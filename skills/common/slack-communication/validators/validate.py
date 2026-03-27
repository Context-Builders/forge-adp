#!/usr/bin/env python3
"""Validator for common/slack-communication skill."""

import json
import sys

SUPPORTED_ACTIONS = {"send_message", "reply_thread", "escalate", "notify_completion", "notify_failure"}
SUPPORTED_URGENCY = {"low", "medium", "high", "critical"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    action = input_data.get("action")
    if not action:
        errors.append("Missing required input: action")
    elif action not in SUPPORTED_ACTIONS:
        errors.append(f"Unsupported action '{action}'. Must be one of: {', '.join(SUPPORTED_ACTIONS)}")

    if not input_data.get("channel"):
        errors.append("Missing required input: channel")

    if not input_data.get("message"):
        errors.append("Missing required input: message")

    if action == "reply_thread" and not input_data.get("thread_ts"):
        errors.append("Missing required input for 'reply_thread': thread_ts")

    urgency = input_data.get("urgency")
    if urgency and urgency not in SUPPORTED_URGENCY:
        errors.append(f"Invalid urgency '{urgency}'. Must be one of: {', '.join(SUPPORTED_URGENCY)}")

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
