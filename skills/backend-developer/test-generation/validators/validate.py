#!/usr/bin/env python3
"""Validator for backend-developer/test-generation skill."""

import json
import sys

SUPPORTED_TEST_TYPES = {"unit", "integration", "e2e"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    source_files = input_data.get("source_files")
    if not source_files:
        errors.append("Missing required input: source_files (must be a non-empty list)")
    elif not isinstance(source_files, list) or len(source_files) == 0:
        errors.append("'source_files' must be a non-empty list of file paths")

    test_types = input_data.get("test_types", [])
    for t in test_types:
        if t not in SUPPORTED_TEST_TYPES:
            errors.append(f"Unsupported test_type '{t}'. Must be one of: {', '.join(SUPPORTED_TEST_TYPES)}")

    coverage_target = input_data.get("coverage_target")
    if coverage_target is not None:
        if not isinstance(coverage_target, (int, float)) or not (0 <= coverage_target <= 100):
            errors.append("'coverage_target' must be a number between 0 and 100")

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
