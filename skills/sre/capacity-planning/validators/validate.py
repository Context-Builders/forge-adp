#!/usr/bin/env python3
"""Validator for sre/capacity-planning skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    planning_horizon = input_data.get("planning_horizon", 90)
    if not isinstance(planning_horizon, int) or planning_horizon <= 0:
        errors.append("'planning_horizon' must be a positive integer (days)")

    growth_pct = input_data.get("growth_assumption_pct", 20)
    if not isinstance(growth_pct, (int, float)) or growth_pct < 0:
        errors.append("'growth_assumption_pct' must be a non-negative number")

    services = input_data.get("services")
    if services is not None and not isinstance(services, list):
        errors.append("'services' must be a list of service name strings")

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
