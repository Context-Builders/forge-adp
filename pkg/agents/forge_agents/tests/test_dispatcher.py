"""
Unit tests for AgentDispatcher.

All Redis and HTTP calls are mocked — no live services required.
"""
import json
from unittest.mock import MagicMock, patch, call
import pytest

from forge_agents.dispatcher import AgentDispatcher
from forge_agents.runtime import Task, TaskStatus


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def make_dispatcher(role="architect", agent_uuid="agent-uuid-1"):
    agent = MagicMock()
    redis_client = MagicMock()
    http = MagicMock()

    d = AgentDispatcher(
        role=role,
        agent=agent,
        agent_uuid=agent_uuid,
        orchestrator_url="http://orch:19080",
        redis_client=redis_client,
        consumer_group=f"dispatch:{role}",
        consumer_name=f"{role}-default",
        stream_name="forge:events",
        claim_idle_ms=60000,
    )
    d.http = http
    return d


def make_stream_message(event_type: str, task_id: str, agent_role: str = "architect") -> tuple:
    """Build a (msg_id, fields) tuple as Redis would return it."""
    payload = json.dumps({"agent_role": agent_role, "skill_name": "requirements-analysis"})
    event = {
        "id": "evt-1",
        "type": event_type,
        "task_id": task_id,
        "payload": payload,
    }
    msg_id = b"1234567890-0"
    fields = {b"type": event_type.encode(), b"data": json.dumps(event).encode()}
    return msg_id, fields


# ---------------------------------------------------------------------------
# _handle_message: non-task.created events are ACKed and ignored
# ---------------------------------------------------------------------------

def test_handle_message_ignores_non_task_created():
    d = make_dispatcher()
    msg_id, fields = make_stream_message("task.completed", "task-1")
    d._handle_message(msg_id, fields)

    d.redis.xack.assert_called_once_with("forge:events", "dispatch:architect", msg_id)
    d.agent.execute_task.assert_not_called()


# ---------------------------------------------------------------------------
# _handle_message: wrong role events are ACKed and ignored
# ---------------------------------------------------------------------------

def test_handle_message_ignores_wrong_role():
    d = make_dispatcher(role="architect")
    msg_id, fields = make_stream_message("task.created", "task-2", agent_role="dba")
    d._handle_message(msg_id, fields)

    d.redis.xack.assert_called_once_with("forge:events", "dispatch:architect", msg_id)
    d.agent.execute_task.assert_not_called()


# ---------------------------------------------------------------------------
# _handle_message: matching role dispatches the task
# ---------------------------------------------------------------------------

def test_handle_message_dispatches_matching_role():
    d = make_dispatcher(role="architect")
    task_id = "task-3"
    msg_id, fields = make_stream_message("task.created", task_id, agent_role="architect")

    task_data = {
        "id": task_id,
        "agent_role": "architect",
        "skill_name": "requirements-analysis",
        "jira_ticket_id": "PROJ-1",
        "input": {"description": "build it"},
        "repo": "org/repo",
    }

    assign_resp = MagicMock()
    assign_resp.status_code = 200
    fetch_resp = MagicMock()
    fetch_resp.status_code = 200
    fetch_resp.json.return_value = task_data
    d.http.post.return_value = assign_resp
    d.http.get.return_value = fetch_resp

    d._handle_message(msg_id, fields)

    d.http.post.assert_called_once_with(
        "http://orch:19080/api/v1/assign",
        json={"task_id": task_id, "agent_id": "agent-uuid-1"},
    )
    d.agent.execute_task.assert_called_once()
    task_arg = d.agent.execute_task.call_args[0][0]
    assert isinstance(task_arg, Task)
    assert task_arg.id == task_id
    assert task_arg.skill_name == "requirements-analysis"


# ---------------------------------------------------------------------------
# _assign_and_execute: assignment conflict (another instance took it)
# ---------------------------------------------------------------------------

def test_assign_and_execute_skips_on_conflict():
    d = make_dispatcher()

    conflict_resp = MagicMock()
    conflict_resp.status_code = 409
    d.http.post.return_value = conflict_resp

    d._assign_and_execute("task-4", b"msg-id-1")

    d.agent.execute_task.assert_not_called()
    # Still ACKs so message doesn't sit in the PEL forever.
    d.redis.xack.assert_called_once_with("forge:events", "dispatch:architect", b"msg-id-1")


# ---------------------------------------------------------------------------
# _assign_and_execute: no agent_uuid skips silently
# ---------------------------------------------------------------------------

def test_assign_and_execute_skips_without_uuid():
    d = make_dispatcher(agent_uuid=None)
    d._assign_and_execute("task-5", b"msg-id-2")

    d.http.post.assert_not_called()
    d.agent.execute_task.assert_not_called()
    d.redis.xack.assert_called_once()


# ---------------------------------------------------------------------------
# _assign_and_execute: task fetch fails after assignment
# ---------------------------------------------------------------------------

def test_assign_and_execute_handles_fetch_failure():
    d = make_dispatcher()

    assign_resp = MagicMock()
    assign_resp.status_code = 200
    fetch_resp = MagicMock()
    fetch_resp.status_code = 404
    d.http.post.return_value = assign_resp
    d.http.get.return_value = fetch_resp

    # Should not raise, and should not call execute_task.
    d._assign_and_execute("task-6", b"msg-id-3")
    d.agent.execute_task.assert_not_called()


# ---------------------------------------------------------------------------
# _handle_message: missing agent_role in payload fetches task from orchestrator
# ---------------------------------------------------------------------------

def test_handle_message_fetches_task_when_role_missing():
    """Covers governance-promoted / unblocked tasks whose event payload
    may not carry agent_role."""
    d = make_dispatcher(role="dba")
    task_id = "task-7"

    # Event payload with no agent_role
    event = {"id": "evt-2", "type": "task.created", "task_id": task_id, "payload": "{}"}
    msg_id = b"999-0"
    fields = {b"type": b"task.created", b"data": json.dumps(event).encode()}

    task_data = {
        "id": task_id,
        "agent_role": "dba",
        "skill_name": "schema-design",
        "jira_ticket_id": "",
        "input": {},
        "repo": "org/repo",
    }

    assign_resp = MagicMock()
    assign_resp.status_code = 200
    fetch_resp = MagicMock()
    fetch_resp.status_code = 200
    fetch_resp.json.return_value = task_data
    d.http.post.return_value = assign_resp
    d.http.get.return_value = fetch_resp

    d._handle_message(msg_id, fields)

    # First GET is the role-check fetch, second is the post-assignment fetch —
    # both hit the same endpoint so we just assert execute_task was called.
    d.agent.execute_task.assert_called_once()


# ---------------------------------------------------------------------------
# _claim_stale: reclaimed messages are dispatched
# ---------------------------------------------------------------------------

def test_claim_stale_dispatches_reclaimed_messages():
    d = make_dispatcher(role="pm")
    task_id = "task-8"
    msg_id = b"111-0"

    payload = json.dumps({"agent_role": "pm", "skill_name": "project-bootstrap"})
    event = {"id": "e", "type": "task.created", "task_id": task_id, "payload": payload}
    fields = {b"type": b"task.created", b"data": json.dumps(event).encode()}

    # xautoclaim returns (next_start_id, messages, deleted_ids)
    d.redis.xautoclaim.return_value = (b"0-0", [(msg_id, fields)], [])

    task_data = {
        "id": task_id, "agent_role": "pm", "skill_name": "project-bootstrap",
        "jira_ticket_id": "", "input": {}, "repo": "org/repo",
    }
    assign_resp = MagicMock()
    assign_resp.status_code = 200
    fetch_resp = MagicMock()
    fetch_resp.status_code = 200
    fetch_resp.json.return_value = task_data
    d.http.post.return_value = assign_resp
    d.http.get.return_value = fetch_resp

    d._claim_stale()

    d.agent.execute_task.assert_called_once()


# ---------------------------------------------------------------------------
# _ensure_consumer_group: BUSYGROUP error is swallowed
# ---------------------------------------------------------------------------

def test_ensure_consumer_group_swallows_busygroup():
    import redis as redis_module
    d = make_dispatcher()
    d.redis.xgroup_create.side_effect = redis_module.exceptions.ResponseError("BUSYGROUP already exists")
    # Should not raise.
    d._ensure_consumer_group()
