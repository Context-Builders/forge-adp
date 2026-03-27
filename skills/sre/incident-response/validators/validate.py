#!/usr/bin/env python3
"""Validator for sre/incident-response skill."""

import json
import sys

SUPPORTED_SEVERITIES = {"sev1", "sev2", "sev3"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("incident_trigger"):
        errors.append("Missing required input: incident_trigger")

    severity = input_data.get("severity")
    if severity and severity not in SUPPORTED_SEVERITIES:
        errors.append(
            f"Invalid severity '{severity}'. "
            f"Must be one of: {', '.join(SUPPORTED_SEVERITIES)}"
        )

    affected_services = input_data.get("affected_services")
    if affected_services is not None and not isinstance(affected_services, list):
        errors.append("'affected_services' must be a list of service name strings")

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
