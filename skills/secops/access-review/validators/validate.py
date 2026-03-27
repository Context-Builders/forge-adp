#!/usr/bin/env python3
"""Validator for secops/access-review skill."""

import json
import sys

SUPPORTED_SCOPES = {"all", "iam", "service-accounts", "github-permissions"}
SUPPORTED_ENVIRONMENTS = {"dev", "staging", "production"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    scope = input_data.get("scope", "all")
    if scope not in SUPPORTED_SCOPES:
        errors.append(
            f"Invalid scope '{scope}'. "
            f"Must be one of: {', '.join(SUPPORTED_SCOPES)}"
        )

    environment = input_data.get("environment", "production")
    if environment not in SUPPORTED_ENVIRONMENTS:
        errors.append(
            f"Invalid environment '{environment}'. "
            f"Must be one of: {', '.join(SUPPORTED_ENVIRONMENTS)}"
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
