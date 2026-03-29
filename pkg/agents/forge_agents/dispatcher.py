"""
Agent Dispatcher — long-running process that subscribes to Redis Streams for
task.created events and executes tasks assigned to this agent's role.

Uses Redis consumer groups for reliable, multi-instance delivery:
  - One group per role (dispatch:{role}) — all instances share work.
  - Stale messages from crashed consumers are reclaimed after CLAIM_IDLE_MS.
  - Assignment via the orchestrator HTTP API prevents two instances racing on
    the same task; the loser ACKs and moves on.

This replaces the previous HTTP-polling approach and also handles tasks that
were never reachable by polling — governance-approved and dependency-unblocked
tasks, which are promoted to status='running' and re-published as task.created
events without going through status='pending'.

Start with:

    AGENT_ROLE=architect python -m forge_agents.dispatcher

Environment variables:
    AGENT_ROLE            (required) e.g. "architect", "pm", "dba"
    ORCHESTRATOR_URL      default: http://localhost:19080
    EVENT_BUS_URL         default: redis://localhost:6379
    FORGE_INSTANCE_ID     default: "default"
    STREAM_NAME           default: forge:events
    CLAIM_IDLE_MS         default: 60000 (reclaim stale pending messages after 60s)
    + all variables required by create_agent() in runner.py
"""
import json
import logging
import os
import sys
import time
from typing import Optional

import httpx
import redis

from .runner import AGENT_CLASSES, create_agent, _register_agent
from .runtime import BaseAgent, Task, TaskStatus
from .logging_config import configure_logging, get_task_logger

logger = logging.getLogger(__name__)


class AgentDispatcher:
    def __init__(
        self,
        role: str,
        agent: BaseAgent,
        agent_uuid: Optional[str],
        orchestrator_url: str,
        redis_client: redis.Redis,
        consumer_group: str,
        consumer_name: str,
        stream_name: str,
        claim_idle_ms: int,
    ):
        self.role = role
        self.agent = agent
        self.agent_uuid = agent_uuid
        self.orchestrator_url = orchestrator_url.rstrip("/")
        self.redis = redis_client
        self.consumer_group = consumer_group
        self.consumer_name = consumer_name
        self.stream_name = stream_name
        self.claim_idle_ms = claim_idle_ms
        self.http = httpx.Client(timeout=30.0)

    def _ensure_consumer_group(self) -> None:
        """Create the consumer group if it doesn't exist.

        MKSTREAM creates the stream itself if it hasn't been written to yet.
        id='$' means new consumers only see messages published after they join.
        """
        try:
            self.redis.xgroup_create(
                self.stream_name, self.consumer_group, id="$", mkstream=True
            )
            logger.info("Created consumer group %s on stream %s", self.consumer_group, self.stream_name)
        except redis.exceptions.ResponseError as exc:
            if "BUSYGROUP" in str(exc):
                logger.info("Consumer group %s already exists", self.consumer_group)
            else:
                raise

    def run(self) -> None:
        self._ensure_consumer_group()
        logger.info(
            "Dispatcher started  role=%s  uuid=%s  group=%s  consumer=%s",
            self.role, self.agent_uuid, self.consumer_group, self.consumer_name,
        )
        while True:
            try:
                self._claim_stale()
                self._read_and_dispatch()
            except redis.exceptions.ConnectionError as exc:
                logger.error("Redis connection error: %s — retrying in 5s", exc)
                time.sleep(5)
            except Exception as exc:
                logger.error("Dispatch loop error: %s", exc)
                time.sleep(1)

    def _read_and_dispatch(self) -> None:
        """Blocking read of new (undelivered) messages from the consumer group."""
        results = self.redis.xreadgroup(
            groupname=self.consumer_group,
            consumername=self.consumer_name,
            streams={self.stream_name: ">"},
            count=10,
            block=5000,  # ms — yields control when the stream is quiet
        )
        if not results:
            return
        for _stream, messages in results:
            for msg_id, fields in messages:
                self._handle_message(msg_id, fields)

    def _claim_stale(self) -> None:
        """Reclaim messages delivered to now-dead consumers and not ACKed."""
        try:
            _, messages, _ = self.redis.xautoclaim(
                self.stream_name,
                self.consumer_group,
                self.consumer_name,
                min_idle_time=self.claim_idle_ms,
                start_id="0-0",
                count=10,
            )
            for msg_id, fields in messages:
                logger.info("Reclaimed stale message %s", msg_id)
                self._handle_message(msg_id, fields)
        except Exception as exc:
            logger.debug("xautoclaim skipped: %s", exc)

    def _decode(self, value) -> str:
        return value.decode() if isinstance(value, bytes) else (value or "")

    def _handle_message(self, msg_id, fields: dict) -> None:
        """Route a stream message: ACK non-matching events, dispatch matching ones."""
        try:
            event_type = self._decode(fields.get(b"type") or fields.get("type"))
            if event_type != "task.created":
                self.redis.xack(self.stream_name, self.consumer_group, msg_id)
                return

            data_raw = self._decode(fields.get(b"data") or fields.get("data"))
            event = json.loads(data_raw) if data_raw else {}

            task_id = event.get("task_id", "")
            if not task_id:
                self.redis.xack(self.stream_name, self.consumer_group, msg_id)
                return

            # agent_role may be absent for governance-promoted / unblocked tasks
            # whose event payload only carries task_id. Fetch to confirm.
            payload = event.get("payload") or {}
            if isinstance(payload, str):
                try:
                    payload = json.loads(payload)
                except json.JSONDecodeError:
                    payload = {}

            agent_role = payload.get("agent_role", "")
            if not agent_role:
                task_data = self._fetch_task(task_id)
                if task_data is None:
                    self.redis.xack(self.stream_name, self.consumer_group, msg_id)
                    return
                agent_role = task_data.get("agent_role", "")

            if agent_role != self.role:
                self.redis.xack(self.stream_name, self.consumer_group, msg_id)
                return

            self._assign_and_execute(task_id, msg_id)

        except Exception as exc:
            logger.error("Error handling message %s: %s", msg_id, exc)
            # Leave unACKed so _claim_stale() redelivers it after idle timeout.

    def _fetch_task(self, task_id: str) -> Optional[dict]:
        try:
            resp = self.http.get(f"{self.orchestrator_url}/api/v1/tasks/{task_id}")
            if resp.status_code == 200:
                return resp.json()
            logger.warning("Fetch task %s returned %s", task_id, resp.status_code)
        except Exception as exc:
            logger.warning("Could not fetch task %s: %s", task_id, exc)
        return None

    def _assign_and_execute(self, task_id: str, msg_id) -> None:
        if not self.agent_uuid:
            logger.warning("No agent UUID — skipping task %s (registry unavailable)", task_id)
            self.redis.xack(self.stream_name, self.consumer_group, msg_id)
            return

        assign = self.http.post(
            f"{self.orchestrator_url}/api/v1/assign",
            json={"task_id": task_id, "agent_id": self.agent_uuid},
        )
        if assign.status_code not in (200, 204):
            # Another dispatcher instance already claimed this task — not an error.
            logger.info(
                "Task %s already assigned (%s) — skipping",
                task_id, assign.status_code,
            )
            self.redis.xack(self.stream_name, self.consumer_group, msg_id)
            return

        # ACK once assignment is confirmed. The task is now 'running' in the
        # orchestrator. If execution crashes the task stays 'running'; recovery
        # (timeout-based re-queuing) is a separate concern.
        self.redis.xack(self.stream_name, self.consumer_group, msg_id)

        task_data = self._fetch_task(task_id)
        if task_data is None:
            logger.error("Could not fetch task %s after assignment", task_id)
            return

        skill_name = task_data.get("skill_name", "")
        tlog = get_task_logger(logger, task_id, self.role)
        tlog.info("executing task", extra={"skill_name": skill_name})

        raw_input = task_data.get("input")
        if isinstance(raw_input, str):
            try:
                input_payload = json.loads(raw_input)
            except json.JSONDecodeError:
                input_payload = {}
        else:
            input_payload = raw_input or {}

        task = Task(
            id=task_id,
            jira_ticket_id=task_data.get("jira_ticket_id", ""),
            skill_name=skill_name,
            input_payload=input_payload,
            status=TaskStatus.QUEUED,
        )
        self.agent.execute_task(task, task_data.get("repo", ""))


def main() -> None:
    configure_logging("dispatcher")

    role = os.environ.get("AGENT_ROLE")
    if not role:
        logger.error("AGENT_ROLE is required")
        sys.exit(1)

    if role not in AGENT_CLASSES:
        logger.error("Unknown AGENT_ROLE %r. Known roles: %s", role, sorted(AGENT_CLASSES))
        sys.exit(1)

    orchestrator_url = os.environ.get("ORCHESTRATOR_URL", "http://localhost:19080")
    event_bus_url = os.environ.get("EVENT_BUS_URL", "redis://localhost:6379")
    stream_name = os.environ.get("STREAM_NAME", "forge:events")
    claim_idle_ms = int(os.environ.get("CLAIM_IDLE_MS", "60000"))
    instance_id = os.environ.get("FORGE_INSTANCE_ID", "default")

    redis_client = redis.Redis.from_url(event_bus_url)

    agent_uuid = _register_agent(role, instance_id)
    if not agent_uuid:
        logger.warning(
            "Registry unavailable — dispatcher will run without task assignment. "
            "Start the registry and restart this process to enable task assignment."
        )

    agent = create_agent(role)

    dispatcher = AgentDispatcher(
        role=role,
        agent=agent,
        agent_uuid=agent_uuid,
        orchestrator_url=orchestrator_url,
        redis_client=redis_client,
        consumer_group=f"dispatch:{role}",
        consumer_name=f"{role}-{instance_id}",
        stream_name=stream_name,
        claim_idle_ms=claim_idle_ms,
    )
    dispatcher.run()


if __name__ == "__main__":
    main()
