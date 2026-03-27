#!/usr/bin/env python3
"""Validator for pm/status-reporting skill."""

import json
import sys

SUPPORTED_AUDIENCES = {"team", "stakeholders", "executives"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("sprint_id"):
        errors.append("Missing required input: sprint_id")

    audience = input_data.get("report_audience", "team")
    if audience not in SUPPORTED_AUDIENCES:
        errors.append(
            f"Invalid report_audience '{audience}'. "
            f"Must be one of: {', '.join(SUPPORTED_AUDIENCES)}"
        )

    include_blockers = input_data.get("include_blockers")
    if include_blockers is not None and not isinstance(include_blockers, bool):
        errors.append("'include_blockers' must be a boolean (true or false)")

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
