"""
DevOps Agent implementation.
"""
from .runtime import BaseAgent, Skill, SkillContext, LLMProvider, AgentIdentity


class DeploymentSkill(Skill):
    """Create and update deployment configuration for services."""

    @property
    def name(self) -> str:
        return "deployment"

    @property
    def description(self) -> str:
        return "Create and update deployment configuration for services"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        service_name = context.task.input_payload.get("service_name", "")
        environment = context.task.input_payload.get("environment", "dev")
        image_tag = context.task.input_payload.get("image_tag", "latest")
        config_overrides = context.task.input_payload.get("config_overrides", {})

        architecture = context.plan_documents.get("ARCHITECTURE.md", "")
        observability = context.plan_documents.get("OBSERVABILITY.md", "")

        config_overrides_text = ""
        if config_overrides:
            import json
            config_overrides_text = f"\nConfig overrides to apply:\n```json\n{json.dumps(config_overrides, indent=2)}\n```"

        observability_section = ""
        if observability:
            observability_section = f"\n## Observability Requirements\n{observability[:1500]}"

        messages = [
            {
                "role": "user",
                "content": f"""Generate deployment configuration for the following service:

Ticket ID: {ticket_id}
Service Name: {service_name}
Environment: {environment}
Image Tag: {image_tag}{config_overrides_text}

## Architecture Context
{architecture}
{observability_section}

Based on the architecture context above, determine whether this project uses Kubernetes/Helm, \
Docker Compose, or another deployment mechanism, then generate the appropriate deployment manifests.

Please provide:
1. **Deployment manifests** — complete, environment-specific configuration (Kubernetes Deployment + \
Service + Ingress, Helm values override file, or Docker Compose service block — whichever fits \
the project's stack)
2. **Health check configuration** — liveness and readiness probes, startup probes if needed, \
timeout and threshold values appropriate for the service
3. **Resource limits** — CPU requests/limits and memory requests/limits sized for the target \
environment ({environment})
4. **Environment variables** — all required env vars with sensible defaults or references to \
secrets/config maps; flag any that must be set before deployment
5. **Rollback steps** — ordered, copy-paste-ready commands or steps to roll back this deployment \
if it fails
6. **Questions** — list any open questions or assumptions that must be confirmed before this \
configuration is applied in production

Format each section clearly with headings.
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000)

        return {
            "deployment_config": response,
            "health_checks": self._extract_section(response, "Health check"),
            "rollback_steps": self._extract_section(response, "Rollback"),
            "environment": environment,
            "questions": self._extract_questions(response),
        }

    def _extract_section(self, response: str, keyword: str) -> str:
        """Best-effort extraction of a named section from the LLM response."""
        lines = response.splitlines()
        in_section = False
        collected: list[str] = []
        for line in lines:
            if keyword.lower() in line.lower() and line.startswith("#"):
                in_section = True
                continue
            if in_section:
                if line.startswith("#") and collected:
                    break
                collected.append(line)
        return "\n".join(collected).strip()

    def _extract_questions(self, response: str) -> list[str]:
        """Extract questions listed in the LLM response."""
        questions: list[str] = []
        in_questions = False
        for line in response.splitlines():
            if "question" in line.lower() and line.startswith("#"):
                in_questions = True
                continue
            if in_questions:
                if line.startswith("#"):
                    break
                stripped = line.strip().lstrip("-*•").strip()
                if stripped:
                    questions.append(stripped)
        return questions


class InfrastructureSkill(Skill):
    """Create and manage cloud infrastructure using IaC."""

    @property
    def name(self) -> str:
        return "infrastructure"

    @property
    def description(self) -> str:
        return "Create and manage cloud infrastructure using Infrastructure as Code"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        description = context.task.input_payload.get("description", "")
        cloud_provider = context.task.input_payload.get("cloud_provider", "aws")
        resources_required = context.task.input_payload.get("resources_required", [])
        environment = context.task.input_payload.get("environment", "dev")

        architecture = context.plan_documents.get("ARCHITECTURE.md", "")

        resources_text = ""
        if resources_required:
            resources_text = "\n".join(f"- {r}" for r in resources_required)
        else:
            resources_text = "Not specified — infer from the description and architecture context."

        messages = [
            {
                "role": "user",
                "content": f"""Generate Terraform HCL to provision the following infrastructure:

Ticket ID: {ticket_id}
Description: {description}
Cloud Provider: {cloud_provider}
Environment: {environment}

Required resources:
{resources_text}

## Architecture Context
{architecture}

Generate production-quality Terraform HCL that:
1. **Follows project patterns** — infer naming conventions, module structure, and tagging \
strategy from the architecture context; be consistent with existing infrastructure
2. **Proper tagging** — apply tags for environment, project, owner, cost-centre, and \
managed-by=terraform on every taggable resource
3. **Security groups / firewall rules** — principle of least privilege; only open ports \
that are strictly required; restrict ingress to known CIDR blocks where possible
4. **IAM policies** — least-privilege IAM roles and policies; no wildcard actions on \
sensitive services; prefer managed policies over inline where appropriate
5. **Variable parameterisation** — use input variables for environment-specific values \
(instance sizes, replica counts, etc.) so the same code applies to dev/staging/production

Please provide:
1. **Terraform HCL** — complete, ready-to-apply `.tf` file content, including `provider`, \
`resource`, `variable`, `output`, and `locals` blocks as needed
2. **Resource list** — bullet list of every cloud resource that will be created
3. **Estimated cost notes** — rough order-of-magnitude cost guidance per resource based \
on typical {cloud_provider} pricing (acknowledge that actual costs depend on usage)
4. **Security considerations** — any security trade-offs, open questions about network \
topology, or items that require a security review before applying to production
5. **Questions** — list any assumptions or open questions that must be answered before \
this Terraform is applied

Format each section clearly with headings.
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000)

        return {
            "terraform_hcl": self._extract_hcl(response),
            "resource_list": self._extract_resource_list(response),
            "estimated_cost_notes": self._extract_section(response, "Estimated cost"),
            "security_considerations": self._extract_section(response, "Security consideration"),
            "questions": self._extract_questions(response),
        }

    def _extract_hcl(self, response: str) -> str:
        """Extract Terraform HCL code blocks from the LLM response."""
        import re
        blocks = re.findall(r"```(?:hcl|terraform)?\n(.*?)```", response, re.DOTALL)
        if blocks:
            return "\n\n".join(b.strip() for b in blocks)
        return response

    def _extract_resource_list(self, response: str) -> list[str]:
        """Extract the bullet-list of resources from the LLM response."""
        resources: list[str] = []
        in_section = False
        for line in response.splitlines():
            if "resource list" in line.lower() and line.startswith("#"):
                in_section = True
                continue
            if in_section:
                if line.startswith("#") and resources:
                    break
                stripped = line.strip().lstrip("-*•").strip()
                if stripped:
                    resources.append(stripped)
        return resources

    def _extract_section(self, response: str, keyword: str) -> str:
        """Best-effort extraction of a named section from the LLM response."""
        lines = response.splitlines()
        in_section = False
        collected: list[str] = []
        for line in lines:
            if keyword.lower() in line.lower() and line.startswith("#"):
                in_section = True
                continue
            if in_section:
                if line.startswith("#") and collected:
                    break
                collected.append(line)
        return "\n".join(collected).strip()

    def _extract_questions(self, response: str) -> list[str]:
        """Extract questions listed in the LLM response."""
        questions: list[str] = []
        in_questions = False
        for line in response.splitlines():
            if "question" in line.lower() and line.startswith("#"):
                in_questions = True
                continue
            if in_questions:
                if line.startswith("#"):
                    break
                stripped = line.strip().lstrip("-*•").strip()
                if stripped:
                    questions.append(stripped)
        return questions


class DevOpsAgent(BaseAgent):
    """DevOps Agent - manages deployments and cloud infrastructure."""

    @property
    def role(self) -> str:
        return "devops"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        # Register skills
        self.register_skill(DeploymentSkill())
        self.register_skill(InfrastructureSkill())
