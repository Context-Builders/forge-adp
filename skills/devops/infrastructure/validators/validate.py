#!/usr/bin/env python3
"""Validator for devops/infrastructure skill."""

import json
import sys

SUPPORTED_CLOUD_PROVIDERS = {"aws", "gcp", "azure"}
SUPPORTED_IAC_TOOLS = {"terraform", "pulumi"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    cloud_provider = input_data.get("cloud_provider")
    if cloud_provider and cloud_provider not in SUPPORTED_CLOUD_PROVIDERS:
        errors.append(
            f"Unsupported cloud_provider '{cloud_provider}'. "
            f"Must be one of: {', '.join(SUPPORTED_CLOUD_PROVIDERS)}"
        )

    iac_tool = input_data.get("iac_tool")
    if iac_tool and iac_tool not in SUPPORTED_IAC_TOOLS:
        errors.append(
            f"Unsupported iac_tool '{iac_tool}'. "
            f"Must be one of: {', '.join(SUPPORTED_IAC_TOOLS)}"
        )

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
