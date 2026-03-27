#!/usr/bin/env python3
"""Validator for backend-developer/service-integration skill."""

import json
import sys

SUPPORTED_INTEGRATION_TYPES = {
    "rest_client", "grpc_client", "sdk_wrapper", "message_consumer", "message_producer"
}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    integration_type = input_data.get("integration_type")
    if integration_type and integration_type not in SUPPORTED_INTEGRATION_TYPES:
        errors.append(
            f"Unsupported integration_type '{integration_type}'. "
            f"Must be one of: {', '.join(SUPPORTED_INTEGRATION_TYPES)}"
        )

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
