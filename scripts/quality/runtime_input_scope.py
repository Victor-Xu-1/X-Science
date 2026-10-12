"""Resolve exact non-embedded runtime inputs to their actual Go consumers.

Declarations add roots to the normal production/test dependency closure. They
never exempt runtime tests; unknown paths and unavailable owners stay broad.
"""

from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
import re


CONTRACT = "scripts/quality/runtime_input_scope.json"


def _asset_path(item: object) -> str:
    if (not isinstance(item, str) or not item.startswith(("assets/", "skills/"))
            or PurePosixPath(item).as_posix() != item
            or any(part in {"", ".", ".."} for part in item.split("/"))
            or any(c in item for c in "\\*?[]\x00\r\n") or item.endswith(".go")
            or PurePosixPath(item).name in {"go.mod", "go.sum", "go.work", "go.work.sum"}):
        raise ValueError("runtime manifest requires exact contained asset paths")
    return item


def _manifest_paths(repo: Path, declaration: dict) -> set[str]:
    """Expand one existing exact manifest, never a directory or glob grant.

    These records declare consumers only. The normal asset-integrity gates
    still verify current bytes, checksums, licenses and runtime semantics.
    """
    if (not isinstance(declaration, dict) or set(declaration) != {"path", "root", "kind"}
            or declaration["kind"] not in {"asset", "conda-catalog"}):
        raise ValueError("invalid runtime manifest declaration")
    manifest, root = _asset_path(declaration["path"]), _asset_path(declaration["root"])
    source = repo / manifest
    if (source.is_symlink() or not source.resolve().is_relative_to(repo.resolve())
            or not source.is_file() or source.stat().st_size > 4 * 1024 * 1024):
        raise ValueError("invalid runtime manifest source")
    value = json.loads(source.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError("invalid runtime manifest value")
    entries = value.get("files" if declaration["kind"] == "asset" else "runtimes")
    schema = 1 if declaration["kind"] == "asset" else 2
    if (type(value.get("schemaVersion")) is not int or value["schemaVersion"] != schema
            or not isinstance(entries, list) or not 0 < len(entries) <= 4096):
        raise ValueError("invalid runtime manifest entries")
    result = {manifest}
    seen = set()
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError("invalid runtime manifest entry")
        if declaration["kind"] == "asset":
            if (set(entry) != {"path", "sha256", "bytes"} or type(entry["bytes"]) is not int
                    or entry["bytes"] < 0 or not isinstance(entry["sha256"], str)
                    or not re.fullmatch(r"[0-9a-f]{64}", entry["sha256"])):
                raise ValueError("invalid runtime asset record")
            relatives = [entry["path"]]
        else:
            relatives = [entry.get(key) for key in ("manifestPath", "explicitPath", "licensesPath")]
        for relative in relatives:
            if not isinstance(relative, str) or relative.startswith("/"):
                raise ValueError("invalid runtime manifest relative path")
            current = _asset_path(root + "/" + relative)
            if current in seen:
                raise ValueError("duplicate runtime manifest file")
            seen.add(current)
            target = repo / current
            if (target.is_symlink() or not target.resolve().is_relative_to(repo.resolve())
                    or not target.is_file()):
                raise ValueError("runtime manifest file is not a contained regular input")
            result.add(current)
    return result


def load(repo: Path) -> dict[str, set[str]]:
    path = repo / CONTRACT
    if not path.exists():
        return {}
    if path.is_symlink() or path.stat().st_size > 65536:
        raise ValueError("invalid runtime input scope file")
    value = json.loads(path.read_text(encoding="utf-8"))
    if (not isinstance(value, dict) or set(value) != {"schema", "groups"}
            or value["schema"] != "x-science.runtime-input-scope.v1"
            or not isinstance(value["groups"], list) or not value["groups"]):
        raise ValueError("invalid runtime input scope contract")
    result: dict[str, set[str]] = {}
    explicit_paths = set()
    names = set()
    for group in value["groups"]:
        if (not isinstance(group, dict) or set(group) - {"name", "paths", "packages", "manifests"}
                or not {"name", "paths", "packages"}.issubset(group)
                or not isinstance(group["name"], str) or not group["name"]
                or group["name"] in names
                or not isinstance(group["paths"], list) or not group["paths"]
                or not isinstance(group["packages"], list) or not group["packages"]):
            raise ValueError("invalid runtime input scope group")
        names.add(group["name"])
        for package in group["packages"]:
            if (not isinstance(package, str) or not package
                    or package.startswith(("/", ".")) or ".." in package.split("/")
                    or any(c in package for c in "\\*?[]\x00\r\n ")
                    or PurePosixPath(package).as_posix() != package):
                raise ValueError("invalid runtime input package owner")
        if len(set(group["packages"])) != len(group["packages"]):
            raise ValueError("duplicate runtime package owner")
        for item in group["paths"]:
            if (not isinstance(item, str) or not (
                    item.startswith(("assets/", "skills/")) or item in {"internal/logoassets/SOURCE.md", "internal/agentruntime/assets/x_science_platform_policy.md"})
                    or PurePosixPath(item).as_posix() != item
                    or any(part in {".", ".."} for part in item.split("/"))
                    or any(c in item for c in "\\*?[]\x00\r\n")
                    or item.endswith(".go") or item in explicit_paths
                    or PurePosixPath(item).name in {"go.mod", "go.sum", "go.work", "go.work.sum"}):
                raise ValueError("runtime input ownership requires unique exact asset paths")
            explicit_paths.add(item)
            result.setdefault(item, set()).update(group["packages"])
        if "manifests" in group:
            if not isinstance(group["manifests"], list) or not group["manifests"]:
                raise ValueError("runtime manifests must be a nonempty explicit list")
            for declaration in group["manifests"]:
                for item in _manifest_paths(repo, declaration):
                    result.setdefault(item, set()).update(group["packages"])
    return result
