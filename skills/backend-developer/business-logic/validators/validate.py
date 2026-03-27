#!/usr/bin/env python3
"""Validator for backend-developer/business-logic skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")
    elif not isinstance(input_data["jira_ticket"], str):
        errors.append("'jira_ticket' must be a string (e.g. 'PAY-1234')")

    existing_models = input_data.get("existing_models")
    if existing_models is not None and not isinstance(existing_models, list):
        errors.append("'existing_models' must be a list of file path strings")

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
