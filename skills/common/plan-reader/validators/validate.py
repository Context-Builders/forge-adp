#!/usr/bin/env python3
"""Validator for common/plan-reader skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    documents = input_data.get("documents")
    if not documents:
        errors.append("Missing required input: documents (must be a non-empty list)")
    elif not isinstance(documents, list):
        errors.append("'documents' must be a list of document name strings")
    elif len(documents) == 0:
        errors.append("'documents' list must not be empty")

    if not input_data.get("repo") and not input_data.get("confluence_space"):
        errors.append("At least one source must be provided: 'repo' or 'confluence_space'")

    if input_data.get("repo") and "/" not in input_data["repo"]:
        errors.append("'repo' must be in 'owner/repo' format")

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
