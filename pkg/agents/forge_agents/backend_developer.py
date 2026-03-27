"""
Backend Developer Agent implementation.
"""
from .runtime import BaseAgent, Skill, SkillContext, LLMProvider, AgentIdentity


class APIImplementationSkill(Skill):
    """Implement REST API endpoints from specifications."""

    @property
    def name(self) -> str:
        return "api-implementation"

    @property
    def description(self) -> str:
        return "Implement REST API endpoints from OpenAPI specifications"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        api_contract = context.task.input_payload.get("api_contract", "")
        if not api_contract and "API_CONTRACTS.md" in context.plan_documents:
            api_contract = context.plan_documents["API_CONTRACTS.md"]

        messages = [
            {
                "role": "user",
                "content": f"""
Implement the following API endpoint based on this contract:

{api_contract}

Requirements:
- Follow the project's architecture patterns
- Include proper error handling
- Add input validation
- Write unit tests

Respond with:
1. Implementation code
2. Unit tests
3. Any questions or concerns
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000)

        return {
            "implementation": response,
            "files_to_create": self._parse_files(response),
            "questions": self._extract_questions(response)
        }

    def _parse_files(self, response: str) -> list[dict]:
        """Parse file content from LLM response."""
        files = []
        # TODO: Extract code blocks and file paths from response
        return files

    def _extract_questions(self, response: str) -> list[str]:
        """Extract any questions the agent has."""
        questions = []
        # TODO: Extract questions from response
        return questions


class BusinessLogicSkill(Skill):
    """Implement domain business logic and validation."""

    @property
    def name(self) -> str:
        return "business-logic"

    @property
    def description(self) -> str:
        return "Implement domain rules and validation logic"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        messages = [
            {
                "role": "user",
                "content": f"""
Implement business logic for: {context.task.input_payload.get('description', '')}

Requirements from ticket:
{context.task.input_payload.get('acceptance_criteria', '')}

Follow the domain model and validation patterns established in this project.
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000)

        return {
            "implementation": response,
            "files_to_create": self._parse_files(response)
        }

    def _parse_files(self, response: str) -> list[dict]:
        return []


class BackendDeveloperAgent(BaseAgent):
    """Backend Developer Agent - implements server-side logic and APIs."""

    @property
    def role(self) -> str:
        return "backend-developer"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        # Register skills
        self.register_skill(APIImplementationSkill())
        self.register_skill(BusinessLogicSkill())
