#!/usr/bin/env python3
"""Validator for dba/query-optimization skill."""

import json
import sys


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    slow_queries = input_data.get("slow_queries")
    if not slow_queries:
        errors.append("Missing required input: slow_queries (must be a non-empty list)")
    elif not isinstance(slow_queries, list) or len(slow_queries) == 0:
        errors.append("'slow_queries' must be a non-empty list of SQL strings")
    else:
        for i, q in enumerate(slow_queries):
            if not isinstance(q, str) or not q.strip():
                errors.append(f"slow_queries[{i}] must be a non-empty SQL string")

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
