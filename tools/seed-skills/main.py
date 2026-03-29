"""
Seed the registry with agent and skill metadata.

Agents are derived from the known roles in the skills/ directory.
Skills are parsed from skills/<role>/<skill-name>/SKILL.md files.

Usage:
    python tools/seed-skills/main.py [--skills-dir PATH] [--registry-url URL] [--dry-run]
"""
import argparse
import os
import re
import sys
from pathlib import Path

import httpx


KNOWN_ROLES = [
    "architect",
    "backend-developer",
    "dba",
    "devops",
    "frontend-developer",
    "governance",
    "pm",
    "qa",
    "secops",
    "sre",
]


def extract_description(skill_md: str) -> str:
    """Return the first non-empty line under the ## Purpose heading."""
    in_purpose = False
    for line in skill_md.splitlines():
        if re.match(r"^##\s+Purpose", line):
            in_purpose = True
            continue
        if in_purpose:
            if re.match(r"^##", line):
                break
            stripped = line.strip().lstrip("- ").strip()
            if stripped:
                return stripped
    return ""


def load_skills(skills_dir: Path) -> list[dict]:
    skills = []
    for skill_md_path in sorted(skills_dir.rglob("SKILL.md")):
        parts = skill_md_path.parts
        try:
            role_idx = parts.index(skills_dir.name) + 1
            role = parts[role_idx]
            name = parts[role_idx + 1]
        except (ValueError, IndexError):
            continue
        if role == "common":
            continue
        content = skill_md_path.read_text()
        skills.append({
            "role": role,
            "name": name,
            "version": "1.0.0",
            "manifest": {
                "description": extract_description(content),
                "autonomy_level": 0,
            },
            "s3_path": "",
        })
    return skills


def seed_agents(roles: list[str], skills: list[dict], registry_url: str, dry_run: bool) -> None:
    print(f"\nSeeding {len(roles)} agents...")
    ok = failed = 0
    skills_by_role: dict[str, list[str]] = {}
    for s in skills:
        skills_by_role.setdefault(s["role"], []).append(s["name"])

    for role in roles:
        if dry_run:
            print(f"  [dry-run] {role}  skills={skills_by_role.get(role, [])}")
            ok += 1
            continue
        try:
            resp = httpx.post(
                f"{registry_url}/api/v1/agents",
                json={"id": role, "role": role, "status": "idle", "skills": skills_by_role.get(role, [])},
                timeout=10.0,
            )
            if resp.status_code in (200, 201):
                print(f"  OK        {role}")
                ok += 1
            else:
                print(f"  FAILED    {role}  ({resp.status_code}: {resp.text.strip()})")
                failed += 1
        except Exception as exc:
            print(f"  ERROR     {role}  ({exc})")
            failed += 1

    print(f"{ok} seeded, {failed} failed")
    if failed:
        sys.exit(1)


def seed_skills(skills: list[dict], registry_url: str, dry_run: bool) -> None:
    print(f"\nSeeding {len(skills)} skills...")
    ok = skipped = failed = 0
    for skill in skills:
        label = f"{skill['role']}/{skill['name']}"
        if dry_run:
            print(f"  [dry-run] {label}: {skill['manifest']['description'][:60]}")
            ok += 1
            continue
        try:
            resp = httpx.post(
                f"{registry_url}/api/v1/skills",
                json=skill,
                timeout=10.0,
            )
            if resp.status_code in (200, 201):
                print(f"  OK        {label}")
                ok += 1
            elif resp.status_code == 409:
                print(f"  exists    {label}")
                skipped += 1
            else:
                print(f"  FAILED    {label}  ({resp.status_code}: {resp.text.strip()})")
                failed += 1
        except Exception as exc:
            print(f"  ERROR     {label}  ({exc})")
            failed += 1

    print(f"{ok} seeded, {skipped} already exist, {failed} failed")
    if failed:
        sys.exit(1)


def main() -> None:
    parser = argparse.ArgumentParser(description="Seed Forge registry with agent and skill metadata")
    parser.add_argument(
        "--skills-dir",
        default=str(Path(__file__).resolve().parents[2] / "skills"),
        help="Path to skills/ directory (default: repo root skills/)",
    )
    parser.add_argument(
        "--registry-url",
        default=os.environ.get("REGISTRY_URL", "http://localhost:19081"),
        help="Registry base URL",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print what would be seeded without calling the API",
    )
    args = parser.parse_args()

    skills_dir = Path(args.skills_dir)
    if not skills_dir.is_dir():
        print(f"skills dir not found: {skills_dir}", file=sys.stderr)
        sys.exit(1)

    skills = load_skills(skills_dir)
    seed_agents(KNOWN_ROLES, skills, args.registry_url, args.dry_run)
    seed_skills(skills, args.registry_url, args.dry_run)


if __name__ == "__main__":
    main()
