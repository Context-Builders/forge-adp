#!/usr/bin/env python3
"""Validator for dba/migration-execution skill."""

import json
import sys

SUPPORTED_TOOLS = {"goose", "flyway", "liquibase", "alembic", "dbmate", "migrate"}
VALID_ENVIRONMENTS = {"dev", "staging", "production"}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    migration_tool = input_data.get("migration_tool")
    if not migration_tool:
        errors.append("Missing required input: migration_tool")
    elif migration_tool not in SUPPORTED_TOOLS:
        errors.append(
            f"Unrecognized migration_tool '{migration_tool}'. "
            f"Supported: {', '.join(sorted(SUPPORTED_TOOLS))}"
        )

    target_env = input_data.get("target_environment")
    if not target_env:
        errors.append("Missing required input: target_environment")
    elif target_env not in VALID_ENVIRONMENTS:
        errors.append(
            f"Unrecognized target_environment '{target_env}'. "
            f"Valid values: {', '.join(sorted(VALID_ENVIRONMENTS))}"
        )

    if not input_data.get("migration_source"):
        errors.append("Missing required input: migration_source")

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
