#!/usr/bin/env python3
"""Validator for pm/ticket-triage skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    ticket_ids = input_data.get("ticket_ids")
    if not ticket_ids:
        errors.append("Missing required input: ticket_ids (must be a non-empty list)")
    elif not isinstance(ticket_ids, list) or len(ticket_ids) == 0:
        errors.append("'ticket_ids' must be a non-empty list of Jira ticket ID strings")
    else:
        for i, t in enumerate(ticket_ids):
            if not isinstance(t, str) or not t.strip():
                errors.append(f"ticket_ids[{i}] must be a non-empty string")

    if len(ticket_ids or []) > 50:
        errors.append("'ticket_ids' list exceeds maximum batch size of 50 tickets")

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
