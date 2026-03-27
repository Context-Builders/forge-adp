#!/usr/bin/env python3
"""Validator for qa/bug-reporting skill."""

import json
import sys

SUPPORTED_SEVERITIES = {"critical", "high", "medium", "low"}
SUPPORTED_ENVIRONMENTS = {"dev", "staging", "production", "local"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("failure_evidence"):
        errors.append("Missing required input: failure_evidence")

    severity = input_data.get("severity")
    if severity and severity not in SUPPORTED_SEVERITIES:
        errors.append(
            f"Invalid severity '{severity}'. "
            f"Must be one of: {', '.join(SUPPORTED_SEVERITIES)}"
        )

    environment = input_data.get("environment")
    if environment and environment not in SUPPORTED_ENVIRONMENTS:
        errors.append(
            f"Unrecognized environment '{environment}'. "
            f"Expected: {', '.join(SUPPORTED_ENVIRONMENTS)}"
        )

    repro_steps = input_data.get("reproduction_steps")
    if repro_steps is not None and not isinstance(repro_steps, list):
        errors.append("'reproduction_steps' must be a list of step strings")

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
