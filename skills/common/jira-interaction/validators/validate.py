#!/usr/bin/env python3
"""Validator for common/jira-interaction skill."""

import json
import sys

SUPPORTED_ACTIONS = {"create_ticket", "update_ticket", "transition_ticket", "add_comment", "get_ticket"}

ACTION_REQUIRED_FIELDS = {
    "create_ticket": ["ticket_data"],
    "update_ticket": ["ticket_id"],
    "transition_ticket": ["ticket_id", "transition"],
    "add_comment": ["ticket_id", "comment"],
    "get_ticket": ["ticket_id"],
}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    action = input_data.get("action")
    if not action:
        errors.append("Missing required input: action")
    elif action not in SUPPORTED_ACTIONS:
        errors.append(f"Unsupported action '{action}'. Must be one of: {', '.join(SUPPORTED_ACTIONS)}")

    if not input_data.get("project_key"):
        errors.append("Missing required input: project_key")

    if action in ACTION_REQUIRED_FIELDS:
        for field in ACTION_REQUIRED_FIELDS[action]:
            if field not in input_data or input_data[field] is None:
                errors.append(f"Missing required input for action '{action}': {field}")

    if "ticket_data" in input_data:
        td = input_data["ticket_data"]
        if not td.get("summary"):
            errors.append("ticket_data missing required field: summary")
        if not td.get("type"):
            errors.append("ticket_data missing required field: type")

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
