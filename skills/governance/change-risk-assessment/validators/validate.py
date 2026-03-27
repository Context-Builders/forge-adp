#!/usr/bin/env python3
"""Validator for governance/change-risk-assessment skill."""

import json
import sys

KNOWN_ROLES = {
    "backend-developer", "frontend-developer", "dba", "devops",
    "qa", "secops", "sre", "pm", "governance",
}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("task_payload"):
        errors.append("Missing required input: task_payload")

    role = input_data.get("agent_role", "")
    if not role:
        errors.append("Missing required input: agent_role")
    elif role not in KNOWN_ROLES:
        errors.append(
            f"Unknown agent_role '{role}'. "
            f"Must be one of: {', '.join(sorted(KNOWN_ROLES))}"
        )

    payload = input_data.get("task_payload", {})
    if isinstance(payload, dict) and not payload.get("skill_name"):
        errors.append("task_payload must include 'skill_name'")

    return len(errors) == 0, errors


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: validate.py <input.json>")
        sys.exit(1)

    with open(sys.argv[1]) as f:
        data = json.load(f)

    ok, errors = validate(data)
    if ok:
        print("✓ Input valid")
        sys.exit(0)
    else:
        for err in errors:
            print(f"✗ {err}")
        sys.exit(1)
