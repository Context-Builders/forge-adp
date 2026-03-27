#!/usr/bin/env python3
"""Validator for qa/test-planning skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    test_scope = input_data.get("test_scope")
    if test_scope is not None and not isinstance(test_scope, list):
        errors.append("'test_scope' must be a list of feature/module name strings")

    risk_areas = input_data.get("risk_areas")
    if risk_areas is not None and not isinstance(risk_areas, list):
        errors.append("'risk_areas' must be a list of risk description strings")

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
