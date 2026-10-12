"""Exact ownership declarations for repository verification tools.

Runtime Go ownership is resolved by the caller before these declarations.
Unlisted inputs keep the conservative runtime fallback.
"""

from __future__ import annotations

import json
import ast
from pathlib import Path, PurePosixPath
import re


CONTRACT = "scripts/quality/verification_scope.json"
GROUP_FIELDS = {"name", "paths", "frontend", "checks"}
STANDALONE_GO_CHECKER = "scripts/quality/go_namespace_equivalence.go"


def _validate_packages(packages: object) -> None:
    if not isinstance(packages, list) or not packages:
        raise ValueError("verification Go owners require a nonempty package list")
    for package in packages:
        if (not isinstance(package, str)
                or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._~-]*(?:/[A-Za-z0-9_][A-Za-z0-9._~-]*)*", package)
                or PurePosixPath(package).as_posix() != package):
            raise ValueError("invalid verification Go package owner")
    if len(set(packages)) != len(packages):
        raise ValueError("duplicate verification Go package owner")


def load(repo: Path) -> list[dict]:
    path = repo / CONTRACT
    if not path.exists():
        return []
    if path.is_symlink() or path.stat().st_size > 65536:
        raise ValueError("invalid verification scope contract file")
    value = json.loads(path.read_text(encoding="utf-8"))
    if (not isinstance(value, dict) or set(value) != {"schema", "groups"}
            or value["schema"] != "x-science.verification-scope.v1"
            or not isinstance(value["groups"], list)):
        raise ValueError("invalid verification scope contract")
    names: set[str] = set()
    paths: set[str] = set()
    for group in value["groups"]:
        if (not isinstance(group, dict) or not GROUP_FIELDS.issubset(group)
                or set(group) - GROUP_FIELDS - {"packages"}
                or not isinstance(group["name"], str)
                or not re.fullmatch(r"[a-z][a-z0-9-]{0,63}", group["name"])
                or group["name"] in names or type(group["frontend"]) is not bool
                or not isinstance(group["paths"], list) or not group["paths"]
                or not isinstance(group["checks"], list) or not group["checks"]):
            raise ValueError("invalid verification scope group")
        names.add(group["name"])
        if "packages" in group:
            _validate_packages(group["packages"])
        for item in group["paths"]:
            if (not isinstance(item, str) or not (
                    item.startswith(("scripts/", ".github/", "docs/governance/", "docs/licenses/", "docs/compatibility/"))
                    or item.startswith(("assets/", "skills/")) and "packages" in group
                    or item in {".env.example", "docs/non-web-asset-boundary.json"})
                    or PurePosixPath(item).as_posix() != item
                    or any(part in {".", ".."} for part in item.split("/"))
                    or any(char in item for char in "\\*?[]\x00\r\n")
                    or PurePosixPath(item).name in {"go.mod", "go.sum", "go.work", "go.work.sum"}
                    or item.endswith(".go") and item != STANDALONE_GO_CHECKER or item in paths):
                raise ValueError("verification ownership requires unique exact tooling paths")
            paths.add(item)
        for command in group["checks"]:
            if (not isinstance(command, list) or len(command) < 2
                    or any(not isinstance(arg, str) or not arg or "\x00" in arg for arg in command)
                    or command[0] not in {"python3", "go", "npm", "node", "bash"}):
                raise ValueError("invalid verification scope check")
    if CONTRACT not in paths:
        raise ValueError("verification scope contract must retain its own verification group")
    return value["groups"]


def package_owners(groups: list[dict]) -> dict[str, set[str]]:
    """Go roots and checker ownership share one validated declaration."""
    return {path: set(group["packages"]) for group in groups if "packages" in group
            for path in group["paths"]}


def matched(repo: Path, changed: list[str]) -> list[dict]:
    paths = set(changed)
    return [group for group in load(repo) if paths.intersection(group["paths"])]


def checks(groups: list[dict]) -> list[list[str]]:
    result: list[list[str]] = []
    for group in groups:
        for command in group["checks"]:
            if command not in result:
                result.append(command)
    return result


def check_python_syntax(repo: Path, paths: list[str]) -> None:
    """Parse exact Python inputs without importing optional scientific engines.

    This is syntax evidence only, never scientific execution or unit-test
    acceptance. Runtime declarations still select each real Go consumer.
    """
    if not 0 < len(paths) <= 64:
        raise ValueError("Python syntax inputs must be an explicit bounded list")
    for value in paths:
        relative = PurePosixPath(value)
        target = repo / value
        if (relative.is_absolute() or relative.as_posix() != value or "\\" in value
                or any(part in {"", ".", ".."} for part in value.split("/"))
                or relative.suffix != ".py" or target.is_symlink()
                or not target.resolve().is_relative_to(repo.resolve())
                or not target.is_file() or target.stat().st_size > 4 * 1024 * 1024):
            raise ValueError("Python syntax input must be an exact contained regular file")
        ast.parse(target.read_text(encoding="utf-8"), filename=value)


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--python-syntax", nargs="+", required=True)
    arguments = parser.parse_args()
    check_python_syntax(Path.cwd(), arguments.python_syntax)
    print("Exact Python source syntax checked; no scientific engines executed.")
