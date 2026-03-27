#!/usr/bin/env python3
"""Validator for dba/schema-migration skill."""

import json
import sys

SUPPORTED_TOOLS = {"goose", "flyway", "liquibase", "alembic", "dbmate", "migrate"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    if not input_data.get("jira_ticket"):
        errors.append("Missing required input: jira_ticket")

    migration_tool = input_data.get("migration_tool")
    if migration_tool and migration_tool not in SUPPORTED_TOOLS:
        errors.append(
            f"Unrecognized migration_tool '{migration_tool}'. "
            f"Supported: {', '.join(SUPPORTED_TOOLS)}"
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
