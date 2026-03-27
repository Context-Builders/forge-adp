#!/usr/bin/env python3
"""Validator for frontend-developer/page-implementation skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    route_path = input_data.get("route_path")
    if route_path and not route_path.startswith("/"):
        errors.append("'route_path' must start with '/' (e.g. '/payments/:id')")

    data_requirements = input_data.get("data_requirements")
    if data_requirements is not None and not isinstance(data_requirements, list):
        errors.append("'data_requirements' must be a list of API endpoint strings")

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
