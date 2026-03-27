#!/usr/bin/env python3
"""Validator for qa/test-execution skill."""

import json
import sys

SUPPORTED_SUITES = {"unit", "integration", "e2e", "regression", "smoke"}
SUPPORTED_ENVIRONMENTS = {"dev", "staging"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    test_suite = input_data.get("test_suite")
    if not test_suite:
        errors.append("Missing required input: test_suite")
    elif test_suite not in SUPPORTED_SUITES:
        errors.append(
            f"Invalid test_suite '{test_suite}'. "
            f"Must be one of: {', '.join(SUPPORTED_SUITES)}"
        )

    target_env = input_data.get("target_environment", "staging")
    if target_env not in SUPPORTED_ENVIRONMENTS:
        errors.append(
            f"Invalid target_environment '{target_env}'. "
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
