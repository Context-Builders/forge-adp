"""
Unit tests for the Forge Agent Runtime.

Covers:
  - BaseAgent.execute_task: happy path, unknown skill, exception handling
  - MemoryStore.store_memory: auto-promotion to project scope at high confidence
  - MemoryStore.get_relevant_memories: returns merged role + project memories
  - PlanReader: local-disk vs GitHub routing, get_config, get_platform_repos
"""
import json
import os
from unittest.mock import MagicMock, patch, PropertyMock
import pytest

from forge_agents.runtime import (
    AgentIdentity, BaseAgent, LLMProvider, MemoryStore,
    PlanReader, Skill, SkillContext, Task, TaskStatus,
)


# ---------------------------------------------------------------------------
# Fixtures / minimal concrete implementations
# ---------------------------------------------------------------------------

class _EchoSkill(Skill):
    """Skill that returns its input payload as output."""

    @property
    def name(self) -> str:
        return "echo"

    @property
    def description(self) -> str:
        return "Returns input as output"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        return {"echoed": context.task.input_payload}


class _BoomSkill(Skill):
    """Skill that always raises."""

    @property
    def name(self) -> str:
        return "boom"

    @property
    def description(self) -> str:
        return "Always fails"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        raise RuntimeError("kaboom")


class _ConcreteAgent(BaseAgent):
    @property
    def role(self) -> str:
        return "test-agent"


def make_agent(skills=None):
    identity = AgentIdentity(
        company_id="co-1",
        project_id="proj-1",
        role="test-agent",
        instance_id="inst-1",
    )
    llm = MagicMock(spec=LLMProvider)
    plan_reader = MagicMock(spec=PlanReader)
    plan_reader.load_plans.return_value = {}
    plan_reader.get_config.return_value = {}

    memory = MagicMock(spec=MemoryStore)
    memory.get_relevant_memories.return_value = []

    agent = _ConcreteAgent(
        identity=identity,
        llm_provider=llm,
        plan_reader=plan_reader,
        memory_store=memory,
        event_bus_url="redis://localhost:6379",
    )

    for skill in (skills or []):
        agent.register_skill(skill)

    # Silence Redis publish calls.
    agent._redis_client = MagicMock()

    return agent


def make_task(skill_name="echo", input_payload=None):
    return Task(
        id="task-001",
        jira_ticket_id="PROJ-1",
        skill_name=skill_name,
        input_payload=input_payload or {"description": "test"},
    )


# ---------------------------------------------------------------------------
# BaseAgent.execute_task: happy path
# ---------------------------------------------------------------------------

def test_execute_task_happy_path():
    agent = make_agent(skills=[_EchoSkill()])
    task = make_task("echo", {"description": "hello"})

    result = agent.execute_task(task, "/some/repo")

    assert result.status == TaskStatus.IN_REVIEW
    assert result.output_payload == {"echoed": {"description": "hello"}}
    assert result.error_message is None


# ---------------------------------------------------------------------------
# BaseAgent.execute_task: unknown skill → FAILED
# ---------------------------------------------------------------------------

def test_execute_task_unknown_skill():
    agent = make_agent()
    task = make_task("nonexistent")

    result = agent.execute_task(task, "/some/repo")

    assert result.status == TaskStatus.FAILED
    assert "Unknown skill" in result.error_message


# ---------------------------------------------------------------------------
# BaseAgent.execute_task: skill raises → FAILED with error_message
# ---------------------------------------------------------------------------

def test_execute_task_skill_exception():
    agent = make_agent(skills=[_BoomSkill()])
    task = make_task("boom")

    result = agent.execute_task(task, "/some/repo")

    assert result.status == TaskStatus.FAILED
    assert "kaboom" in result.error_message


# ---------------------------------------------------------------------------
# BaseAgent.execute_task: dependency_outputs popped from input_payload
# ---------------------------------------------------------------------------

def test_execute_task_dependency_outputs_isolated():
    """dependency_outputs must be removed from input_payload before the skill
    sees it, and passed via SkillContext.dependency_outputs instead."""
    captured = {}

    class _CaptureSkill(Skill):
        @property
        def name(self):
            return "capture"
        @property
        def description(self):
            return ""
        def execute(self, context, llm):
            captured["input"] = dict(context.task.input_payload)
            captured["dep_outputs"] = dict(context.dependency_outputs)
            return {}

    agent = make_agent(skills=[_CaptureSkill()])
    task = make_task("capture", {
        "description": "do work",
        "dependency_outputs": {"api-design": {"endpoints": []}},
    })

    agent.execute_task(task, "/repo")

    assert "dependency_outputs" not in captured["input"]
    assert "api-design" in captured["dep_outputs"]


# ---------------------------------------------------------------------------
# BaseAgent.execute_task: task.started and task.completed events published
# ---------------------------------------------------------------------------

def test_execute_task_publishes_events():
    agent = make_agent(skills=[_EchoSkill()])
    task = make_task("echo")

    agent.execute_task(task, "/repo")

    calls = [c[0][0] for c in agent._redis_client.xadd.call_args_list]
    # All xadd calls go to the same stream; check event types in the data field.
    published_types = []
    for c in agent._redis_client.xadd.call_args_list:
        data_str = c[0][1].get("data", "{}")
        data = json.loads(data_str)
        published_types.append(data.get("type"))

    assert "task.started" in published_types
    assert "task.completed" in published_types


# ---------------------------------------------------------------------------
# MemoryStore.store_memory: auto-promotes to project scope at high confidence
# ---------------------------------------------------------------------------

def test_store_memory_auto_promotes_to_project_scope():
    store = MemoryStore.__new__(MemoryStore)
    store.engine = MagicMock()
    conn = MagicMock()
    store.engine.connect.return_value.__enter__ = MagicMock(return_value=conn)
    store.engine.connect.return_value.__exit__ = MagicMock(return_value=False)

    store.store_memory(
        company_id="co-1",
        project_id="proj-1",
        agent_role="architect",
        category="architecture_decision",
        content="Use event sourcing",
        source_tickets=["PROJ-10"],
        confidence=0.9,  # above PROJECT_SCOPE_THRESHOLD
        scope="role",
    )

    call_kwargs = conn.execute.call_args[0][1]
    assert call_kwargs["scope"] == "project", (
        "High-confidence memory should be auto-promoted to project scope"
    )


def test_store_memory_respects_explicit_project_scope():
    store = MemoryStore.__new__(MemoryStore)
    store.engine = MagicMock()
    conn = MagicMock()
    store.engine.connect.return_value.__enter__ = MagicMock(return_value=conn)
    store.engine.connect.return_value.__exit__ = MagicMock(return_value=False)

    store.store_memory(
        company_id="co-1",
        project_id="proj-1",
        agent_role="dba",
        category="data_model",
        content="Use UUID PKs",
        source_tickets=[],
        confidence=0.3,  # low confidence, but explicit project scope
        scope="project",
    )

    call_kwargs = conn.execute.call_args[0][1]
    assert call_kwargs["scope"] == "project"


def test_store_memory_stays_role_scope_at_low_confidence():
    store = MemoryStore.__new__(MemoryStore)
    store.engine = MagicMock()
    conn = MagicMock()
    store.engine.connect.return_value.__enter__ = MagicMock(return_value=conn)
    store.engine.connect.return_value.__exit__ = MagicMock(return_value=False)

    store.store_memory(
        company_id="co-1",
        project_id="proj-1",
        agent_role="qa",
        category="test_pattern",
        content="Use table-driven tests",
        source_tickets=[],
        confidence=0.5,
        scope="role",
    )

    call_kwargs = conn.execute.call_args[0][1]
    assert call_kwargs["scope"] == "role"


# ---------------------------------------------------------------------------
# PlanReader: local-disk routing
# ---------------------------------------------------------------------------

def test_plan_reader_loads_from_local_disk(tmp_path):
    forge_dir = tmp_path / ".forge"
    forge_dir.mkdir()
    (forge_dir / "ARCHITECTURE.md").write_text("# Architecture")
    (forge_dir / "config.yaml").write_text("forge:\n  version: 1\n")

    reader = PlanReader(github_adapter_url="http://github-adapter")
    plans = reader.load_plans(str(tmp_path))

    assert "ARCHITECTURE.md" in plans
    assert plans["ARCHITECTURE.md"] == "# Architecture"
    assert "config.yaml" in plans


def test_plan_reader_skips_missing_files(tmp_path):
    (tmp_path / ".forge").mkdir()
    # Only write one file — others should be silently absent.
    (tmp_path / ".forge" / "PRODUCT.md").write_text("product brief")

    reader = PlanReader(github_adapter_url="http://github-adapter")
    plans = reader.load_plans(str(tmp_path))

    assert "PRODUCT.md" in plans
    assert "ARCHITECTURE.md" not in plans


# ---------------------------------------------------------------------------
# PlanReader: GitHub routing (mocked HTTP)
# ---------------------------------------------------------------------------

def test_plan_reader_loads_from_github():
    reader = PlanReader(github_adapter_url="http://github-adapter")
    reader.client = MagicMock()

    def fake_get(url, params=None):
        resp = MagicMock()
        if params and params.get("path") == ".forge/ARCHITECTURE.md":
            resp.status_code = 200
            resp.json.return_value = {"content": "# Remote Architecture"}
        else:
            resp.status_code = 404
        return resp

    reader.client.get.side_effect = fake_get
    plans = reader.load_plans("org/myrepo")

    assert "ARCHITECTURE.md" in plans
    assert plans["ARCHITECTURE.md"] == "# Remote Architecture"


# ---------------------------------------------------------------------------
# PlanReader.get_config: parses YAML correctly
# ---------------------------------------------------------------------------

def test_get_config_parses_yaml():
    reader = PlanReader(github_adapter_url="http://github-adapter")
    plans = {"config.yaml": "forge:\n  version: 2\n  project_id: proj-1\n"}
    config = reader.get_config(plans)

    assert config["forge"]["version"] == 2
    assert config["forge"]["project_id"] == "proj-1"


def test_get_config_returns_empty_when_missing():
    reader = PlanReader(github_adapter_url="http://github-adapter")
    assert reader.get_config({}) == {}


# ---------------------------------------------------------------------------
# PlanReader.get_platform_repos
# ---------------------------------------------------------------------------

def test_get_platform_repos_returns_list():
    reader = PlanReader(github_adapter_url="http://github-adapter")
    plans = {
        "config.yaml": (
            "forge:\n"
            "  platform:\n"
            "    repos:\n"
            "      - repo: org/api\n"
            "        role: api\n"
            "      - repo: org/ui\n"
            "        role: ui\n"
        )
    }
    repos = reader.get_platform_repos(plans)
    assert len(repos) == 2
    assert repos[0]["role"] == "api"


def test_get_platform_repos_empty_for_single_repo():
    reader = PlanReader(github_adapter_url="http://github-adapter")
    plans = {"config.yaml": "forge:\n  version: 1\n"}
    assert reader.get_platform_repos(plans) == []
