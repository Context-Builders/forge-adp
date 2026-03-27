#!/usr/bin/env python3
"""Validator for frontend-developer/component-implementation skill."""

import json
import sys
import re


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    component_name = input_data.get("component_name")
    if component_name and not re.match(r"^[A-Z][a-zA-Z0-9]+$", component_name):
        errors.append("'component_name' must be in PascalCase (e.g. 'PaymentCard')")

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
