#!/usr/bin/env python3
"""Validator for governance/policy-drift-detection skill."""

import json
import sys
import re

ISO_DURATION_RE = re.compile(r"^P(\d+Y)?(\d+M)?(\d+W)?(\d+D)?(T(\d+H)?(\d+M)?(\d+S)?)?$")


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("project_id"):
        errors.append("Missing required input: project_id")

    period = input_data.get("period", "P30D")
    if not ISO_DURATION_RE.match(period):
        errors.append(f"Invalid ISO 8601 duration: '{period}'")

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
