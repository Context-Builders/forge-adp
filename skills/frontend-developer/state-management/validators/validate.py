#!/usr/bin/env python3
"""Validator for frontend-developer/state-management skill."""

import json
import sys

SUPPORTED_LIBRARIES = {"redux", "zustand", "jotai", "context", "mobx", "recoil"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    state_library = input_data.get("state_library")
    if state_library and state_library not in SUPPORTED_LIBRARIES:
        errors.append(
            f"Unrecognized state_library '{state_library}'. "
            f"Supported: {', '.join(SUPPORTED_LIBRARIES)}"
        )

    affected_features = input_data.get("affected_features")
    if affected_features is not None and not isinstance(affected_features, list):
        errors.append("'affected_features' must be a list of feature name strings")

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
