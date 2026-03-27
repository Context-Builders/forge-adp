#!/usr/bin/env python3
"""Validator for backend-developer/api-implementation skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    api_contract = input_data.get("api_contract")
    if not api_contract:
        # Fall back to plan document — not an error if omitted
        pass
    elif isinstance(api_contract, dict):
        if not api_contract.get("path"):
            errors.append("api_contract missing required field: path")
        if not api_contract.get("method"):
            errors.append("api_contract missing required field: method")
    elif not isinstance(api_contract, str):
        errors.append("'api_contract' must be a dict (inline spec) or string (document reference)")

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
