#!/usr/bin/env python3
"""Validator for pm/sprint-planning skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("goal_description"):
        errors.append("Missing required input: goal_description")
    elif len(input_data["goal_description"].strip()) < 10:
        errors.append("'goal_description' is too short — provide enough context to decompose")

    capacity = input_data.get("sprint_capacity")
    if capacity is not None:
        if not isinstance(capacity, (int, float)) or capacity <= 0:
            errors.append("'sprint_capacity' must be a positive number")

    existing_backlog = input_data.get("existing_backlog")
    if existing_backlog is not None and not isinstance(existing_backlog, list):
        errors.append("'existing_backlog' must be a list of ticket ID strings")

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
