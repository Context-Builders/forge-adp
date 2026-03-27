"""
End-to-end workflow integration tests.
"""
import pytest
import httpx
import time


ORCHESTRATOR_URL = "http://localhost:19080"
JIRA_ADAPTER_URL = "http://localhost:19090"
GITHUB_ADAPTER_URL = "http://localhost:19091"


@pytest.fixture
def orchestrator():
    return httpx.Client(base_url=ORCHESTRATOR_URL)


@pytest.fixture
def jira_adapter():
    return httpx.Client(base_url=JIRA_ADAPTER_URL)


class TestTicketToTask:
    """Test that Jira tickets create Forge tasks."""

    def test_jira_webhook_creates_task(self, orchestrator, jira_adapter):
        # Simulate Jira webhook
        webhook_payload = {
            "webhookEvent": "jira:issue_created",
            "issue": {
                "key": "TEST-123",
                "fields": {
                    "summary": "Implement GET /users endpoint",
                    "description": "Create a REST endpoint to fetch user by ID",
                    "labels": ["forge"],
                    "priority": {"name": "High"},
                },
            },
        }

        response = jira_adapter.post("/webhook", json=webhook_payload)
        assert response.status_code == 200

        # Wait for task to be created
        time.sleep(2)

        # Check task exists in orchestrator
        response = orchestrator.get("/api/v1/tasks", params={"jira_key": "TEST-123"})
        assert response.status_code == 200
        tasks = response.json()
        assert len(tasks) >= 1
        assert tasks[0]["jira_ticket_id"] == "TEST-123"


class TestAgentExecution:
    """Test agent task execution."""

    def test_backend_agent_creates_pr(self, orchestrator):
        # Create a task
        task = {
            "jira_ticket_id": "TEST-456",
            "agent_role": "backend-developer",
            "skill_name": "api-implementation",
            "priority": 1,
            "input": {
                "description": "Implement GET /health endpoint",
                "api_contract": {
                    "path": "/health",
                    "method": "GET",
                    "response": {"status": "string"},
                },
            },
        }

        response = orchestrator.post("/api/v1/tasks", json=task)
        assert response.status_code == 201

        # In real test, would wait for agent job to complete
        # and verify PR was created
