#!/usr/bin/env python3
"""Validator for devops/deployment skill."""

import json
import sys

SUPPORTED_ENVIRONMENTS = {"dev", "staging", "production"}
SUPPORTED_STRATEGIES = {"rolling", "blue-green", "canary"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    target_env = input_data.get("target_environment", "staging")
    if target_env not in SUPPORTED_ENVIRONMENTS:
        errors.append(
            f"Invalid target_environment '{target_env}'. "
            f"Must be one of: {', '.join(SUPPORTED_ENVIRONMENTS)}"
        )

    strategy = input_data.get("deployment_strategy", "rolling")
    if strategy not in SUPPORTED_STRATEGIES:
        errors.append(
            f"Invalid deployment_strategy '{strategy}'. "
            f"Must be one of: {', '.join(SUPPORTED_STRATEGIES)}"
        )

    spec = input_data.get("deployment_spec")
    if spec:
        if not spec.get("service"):
            errors.append("deployment_spec missing required field: service")
        if not spec.get("image_tag"):
            errors.append("deployment_spec missing required field: image_tag")

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
