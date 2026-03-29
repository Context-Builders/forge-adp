"""
QA Agent implementation.
"""
from .runtime import BaseAgent, Skill, SkillContext, LLMProvider, AgentIdentity


class TestPlanningSkill(Skill):
    """Generate a comprehensive test plan for a feature or release."""

    @property
    def name(self) -> str:
        return "test-planning"

    @property
    def description(self) -> str:
        return "Generate a comprehensive test plan for a feature or release"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        feature_description = context.task.input_payload.get("feature_description", "")
        acceptance_criteria = context.task.input_payload.get("acceptance_criteria", [])
        scope = context.task.input_payload.get("scope", "all")

        qa_standards = context.plan_documents.get("QA_STANDARDS.md", "")
        architecture = context.plan_documents.get("ARCHITECTURE.md", "")
        api_contracts = context.plan_documents.get("API_CONTRACTS.md", "")

        criteria_text = "\n".join(
            f"- {c}" for c in acceptance_criteria
        ) if acceptance_criteria else "None provided"

        scope_instruction = {
            "unit": "Focus on unit tests only.",
            "integration": "Focus on integration tests only.",
            "e2e": "Focus on end-to-end tests only.",
            "all": "Cover unit, integration, and end-to-end tests.",
        }.get(scope, "Cover unit, integration, and end-to-end tests.")

        messages = [
            {
                "role": "user",
                "content": f"""Generate a comprehensive test plan for the following feature.

## Ticket
{ticket_id}

## Feature Description
{feature_description}

## Acceptance Criteria
{criteria_text}

## Test Scope
{scope_instruction}

## QA Standards
{qa_standards}

## Architecture Context
{architecture[:2000] if architecture else "Not provided"}

## API Contracts
{api_contracts[:2000] if api_contracts else "Not provided"}

Produce a structured test plan with the following sections:

1. **test_plan** — broken down into:
   - unit_tests: list of test cases with name and description
   - integration_tests: list of test cases with name and description
   - e2e_tests: list of test cases with name and description
   - edge_cases: list of edge cases to handle
2. **coverage_areas** — list of functional areas covered
3. **risk_areas** — list of high-risk areas requiring extra attention
4. **estimated_effort** — rough effort estimate (e.g., "3 days")
5. **questions** — any open questions or clarifications needed

Respond in structured JSON matching exactly these keys.
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000)

        return {
            "test_plan": self._parse_test_plan(response),
            "coverage_areas": self._extract_list(response, "coverage_areas"),
            "risk_areas": self._extract_list(response, "risk_areas"),
            "estimated_effort": self._extract_field(response, "estimated_effort"),
            "questions": self._extract_list(response, "questions"),
            "raw_response": response,
        }

    def _parse_test_plan(self, response: str) -> dict:
        """Attempt to extract structured test plan from the response."""
        import json
        try:
            data = json.loads(response)
            return data.get("test_plan", {
                "unit_tests": [],
                "integration_tests": [],
                "e2e_tests": [],
                "edge_cases": [],
            })
        except (json.JSONDecodeError, AttributeError):
            return {
                "unit_tests": [],
                "integration_tests": [],
                "e2e_tests": [],
                "edge_cases": [],
            }

    def _extract_list(self, response: str, key: str) -> list:
        """Extract a list field from a JSON response."""
        import json
        try:
            data = json.loads(response)
            return data.get(key, [])
        except (json.JSONDecodeError, AttributeError):
            return []

    def _extract_field(self, response: str, key: str) -> str:
        """Extract a scalar field from a JSON response."""
        import json
        try:
            data = json.loads(response)
            return data.get(key, "")
        except (json.JSONDecodeError, AttributeError):
            return ""


class TestExecutionSkill(Skill):
    """Execute test suites and record results."""

    @property
    def name(self) -> str:
        return "test-execution"

    @property
    def description(self) -> str:
        return "Analyze test execution results and determine whether quality gates pass"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        test_suite = context.task.input_payload.get("test_suite", "all")
        environment = context.task.input_payload.get("environment", "")
        test_results = context.task.input_payload.get("test_results", [])
        coverage_report = context.task.input_payload.get("coverage_report", "")

        qa_standards = context.plan_documents.get("QA_STANDARDS.md", "")

        passed = sum(1 for r in test_results if r.get("status") == "passed")
        failed = sum(1 for r in test_results if r.get("status") == "failed")
        skipped = sum(1 for r in test_results if r.get("status") == "skipped")
        total = len(test_results)

        results_text = "\n".join(
            f"- [{r.get('status', 'unknown').upper()}] {r.get('test_name', 'unknown')}"
            + (f": {r.get('error_message', '')}" if r.get("error_message") else "")
            for r in test_results
        ) if test_results else "No test results provided"

        messages = [
            {
                "role": "user",
                "content": f"""Analyze the following test execution results.

## Ticket
{ticket_id}

## Test Suite
{test_suite}

## Environment
{environment}

## Test Results ({passed} passed, {failed} failed, {skipped} skipped of {total} total)
{results_text}

## Coverage Report
{coverage_report if coverage_report else "Not provided"}

## QA Standards
{qa_standards}

Perform the following analysis:

1. Identify patterns in the failures (common modules, error types, flakiness indicators).
2. Determine whether quality gates pass based on the QA standards and pass rate.
3. Recommend specific actions to resolve failures.
4. Write an executive summary suitable for a release decision.

Respond in structured JSON with exactly these keys:
- passed (int)
- failed (int)
- skipped (int)
- total (int)
- pass_rate (float, e.g. 0.95)
- quality_gate_passed (bool)
- failure_analysis (list of strings describing failure patterns)
- summary (string)
- recommended_actions (list of strings)
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=4000)

        parsed = self._parse_response(response)

        return {
            "passed": parsed.get("passed", passed),
            "failed": parsed.get("failed", failed),
            "skipped": parsed.get("skipped", skipped),
            "total": parsed.get("total", total),
            "pass_rate": parsed.get("pass_rate", round(passed / total, 4) if total else 0.0),
            "quality_gate_passed": parsed.get("quality_gate_passed", failed == 0),
            "failure_analysis": parsed.get("failure_analysis", []),
            "summary": parsed.get("summary", ""),
            "recommended_actions": parsed.get("recommended_actions", []),
            "raw_response": response,
        }

    def _parse_response(self, response: str) -> dict:
        """Parse JSON response from LLM."""
        import json
        try:
            return json.loads(response)
        except (json.JSONDecodeError, AttributeError):
            return {}


class BugReportingSkill(Skill):
    """Transform test failures and error logs into structured Jira bug reports."""

    @property
    def name(self) -> str:
        return "bug-reporting"

    @property
    def description(self) -> str:
        return "Transform test failures and error logs into structured Jira bug reports"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        failures = context.task.input_payload.get("failures", [])
        environment = context.task.input_payload.get("environment", "")
        severity = context.task.input_payload.get("severity", "medium")

        qa_standards = context.plan_documents.get("QA_STANDARDS.md", "")

        failures_text = "\n\n".join(
            f"Test: {f.get('test_name', 'unknown')}\n"
            f"Error: {f.get('error', '')}\n"
            f"Stack Trace:\n{f.get('stack_trace', 'None')}"
            for f in failures
        ) if failures else "No failures provided"

        messages = [
            {
                "role": "user",
                "content": f"""Transform the following test failures into structured Jira bug reports.

## Ticket
{ticket_id}

## Environment
{environment}

## Default Severity
{severity}

## Failures
{failures_text}

## QA Standards
{qa_standards}

For each failure produce a structured bug report. Use your judgment to:
- Write a concise, descriptive title
- Describe the bug clearly in the description
- List step-by-step reproduction steps
- State expected vs actual behaviour
- Assess severity per failure (may differ from the default if evidence warrants)
- Suggest relevant labels (e.g. regression, flaky, environment-specific)

Respond in structured JSON with exactly these keys:
- bug_reports: list of objects, each with:
  - title (string)
  - description (string)
  - steps_to_reproduce (list of strings)
  - expected (string)
  - actual (string)
  - severity (string: critical/high/medium/low)
  - labels (list of strings)
- summary (string — brief overview of all failures)
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=4000)

        parsed = self._parse_response(response)

        return {
            "bug_reports": parsed.get("bug_reports", []),
            "summary": parsed.get("summary", ""),
            "raw_response": response,
        }

    def _parse_response(self, response: str) -> dict:
        """Parse JSON response from LLM."""
        import json
        try:
            return json.loads(response)
        except (json.JSONDecodeError, AttributeError):
            return {}


class QAAgent(BaseAgent):
    """QA Agent - plans, executes, and reports on software quality."""

    @property
    def role(self) -> str:
        return "qa"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        self.register_skill(TestPlanningSkill())
        self.register_skill(TestExecutionSkill())
        self.register_skill(BugReportingSkill())
