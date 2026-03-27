"""
Agent Runner - Entry point for executing agent tasks.
"""
import os
import sys
import json
import logging
from typing import Type

from .runtime import (
    BaseAgent, AgentIdentity, Task, TaskStatus,
    LLMProvider, PlanReader, MemoryStore
)
from .architect import ArchitectAgent
from .backend_developer import BackendDeveloperAgent
from .governance import GovernanceAgent
from .pm import PMAgent

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

AGENT_CLASSES: dict[str, Type[BaseAgent]] = {
    "architect": ArchitectAgent,
    "backend-developer": BackendDeveloperAgent,
    "governance": GovernanceAgent,
    "pm": PMAgent,
}


def create_agent(role: str) -> BaseAgent:
    """Factory function to create an agent instance."""
    if role not in AGENT_CLASSES:
        raise ValueError(f"Unknown agent role: {role}")

    identity = AgentIdentity(
        company_id=os.environ["FORGE_COMPANY_ID"],
        project_id=os.environ["FORGE_PROJECT_ID"],
        role=role,
        instance_id=os.environ.get("FORGE_INSTANCE_ID", "default")
    )

    llm_provider = LLMProvider(
        provider=os.environ.get("LLM_PROVIDER", "anthropic"),
        model=os.environ.get("LLM_MODEL", "claude-sonnet-4-5-20250929")
    )

    plan_reader = PlanReader(
        github_adapter_url=os.environ["GITHUB_ADAPTER_URL"]
    )

    memory_store = MemoryStore(
        database_url=os.environ["DATABASE_URL"]
    )

    agent_class = AGENT_CLASSES[role]
    return agent_class(
        identity=identity,
        llm_provider=llm_provider,
        plan_reader=plan_reader,
        memory_store=memory_store,
        event_bus_url=os.environ["EVENT_BUS_URL"]
    )


def main():
    """Main entry point for the agent runner."""
    role = os.environ.get("AGENT_ROLE")
    task_json = os.environ.get("TASK_PAYLOAD")
    repo = os.environ.get("REPO")

    if not all([role, task_json, repo]):
        logger.error("Missing required environment variables: AGENT_ROLE, TASK_PAYLOAD, REPO")
        sys.exit(1)

    task_data = json.loads(task_json)
    task = Task(
        id=task_data["id"],
        jira_ticket_id=task_data["jira_ticket_id"],
        skill_name=task_data["skill_name"],
        input_payload=task_data["input"],
        status=TaskStatus.QUEUED
    )

    agent = create_agent(role)
    result = agent.execute_task(task, repo)

    print(json.dumps({
        "task_id": result.id,
        "status": result.status.value,
        "output": result.output_payload,
        "error": result.error_message
    }))

    sys.exit(0 if result.status == TaskStatus.IN_REVIEW else 1)


if __name__ == "__main__":
    main()
