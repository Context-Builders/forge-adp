#!/usr/bin/env python3
"""Validator for common/github-interaction skill."""

import json
import sys

SUPPORTED_ACTIONS = {"create_branch", "commit_files", "open_pr", "request_review", "merge_pr"}

ACTION_REQUIRED_FIELDS = {
    "create_branch": ["branch_name"],
    "commit_files": ["branch_name", "files"],
    "open_pr": ["pr_config"],
    "request_review": ["pr_number"],
    "merge_pr": ["pr_number"],
}


def validate(input_data: dict) -> tuple[bool, list[str]]:
    errors = []

    action = input_data.get("action")
    if not action:
        errors.append("Missing required input: action")
    elif action not in SUPPORTED_ACTIONS:
        errors.append(f"Unsupported action '{action}'. Must be one of: {', '.join(SUPPORTED_ACTIONS)}")

    if not input_data.get("repo"):
        errors.append("Missing required input: repo")
    elif "/" not in input_data["repo"]:
        errors.append("repo must be in 'owner/repo' format")

    if action in ACTION_REQUIRED_FIELDS:
        for field in ACTION_REQUIRED_FIELDS[action]:
            if field not in input_data or input_data[field] is None:
                errors.append(f"Missing required input for action '{action}': {field}")

    if "pr_config" in input_data:
        pr = input_data["pr_config"]
        for f in ["title", "base"]:
            if not pr.get(f):
                errors.append(f"pr_config missing required field: {f}")

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
