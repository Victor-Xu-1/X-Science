"""Verify one PR's version transition from exact Git trees without executing proposal code."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.quality.product_version import ProductVersion, next_version, require_next_version
if __package__:
    from .version_provenance import document
else:
    from version_provenance import document

SHA = re.compile(r"[0-9a-f]{40}")
INITIAL_BASE = "0" * 40


def git(root: Path, *arguments: str) -> bytes:
    result = subprocess.run(["git", *arguments], cwd=root, capture_output=True, timeout=15, check=False)
    if result.returncode:
        raise ValueError("product identity is unavailable at the requested commit")
    return result.stdout


def read_version(root: Path, revision: str) -> str:
    if type(revision) is not str or not SHA.fullmatch(revision):
        raise ValueError("version check requires an exact Git commit")
    if git(root, "cat-file", "-t", revision).strip() != b"commit":
        raise ValueError("version check requires a Git commit, not another object")
    entry = git(root, "ls-tree", revision, "--", "product-identity.json").strip().split(b"\t")
    if len(entry) != 2 or entry[1] != b"product-identity.json":
        raise ValueError("product identity is unavailable at the requested commit")
    mode, kind, blob = entry[0].split()
    if mode != b"100644" or kind != b"blob":
        raise ValueError("product identity must be a regular non-executable file")
    if int(git(root, "cat-file", "-s", blob.decode())) > 65_536:
        raise ValueError("product identity exceeds the input size limit")
    identity = document(git(root, "cat-file", "blob", blob.decode()))
    if "version" not in identity:
        raise ValueError("product version is missing")
    return str(ProductVersion.parse(identity["version"]))


def verify_transition(root: Path, base: str, candidate: str, *, initial_main: bool = False) -> dict[str, str]:
    if initial_main:
        if base != INITIAL_BASE:
            raise ValueError("initial main check requires the explicit zero base")
        return {"base": base, "candidate": candidate, "product_version": read_version(root, candidate),
                "result": "PASS", "transition": "initial-main-not-a-PR-merge"}
    previous = read_version(root, base)
    current = read_version(root, candidate)
    ancestry = subprocess.run(["git", "merge-base", "--is-ancestor", base, candidate], cwd=root,
                              capture_output=True, timeout=15, check=False)
    if ancestry.returncode:
        raise ValueError("candidate does not contain current main")
    require_next_version(previous, current)
    return {"base": base, "candidate": candidate, "previous_version": previous,
            "product_version": current, "expected_version": next_version(previous), "result": "PASS"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--base", required=True)
    parser.add_argument("--candidate", required=True)
    parser.add_argument("--initial-main", choices=("true", "false"), default="false")
    args = parser.parse_args()
    try:
        result = verify_transition(args.repo.resolve(), args.base, args.candidate,
                                   initial_main=args.initial_main == "true")
    except (ValueError, OSError, subprocess.TimeoutExpired) as error:
        print(json.dumps({"result": "FAIL", "reason": str(error)}))
        return 1
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
