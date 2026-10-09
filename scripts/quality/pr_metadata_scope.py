"""Separate proved version metadata from behavior changes in exact Git trees."""

from __future__ import annotations

import hashlib
import re
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.packaging.version_provenance import AUDIT, IDENTITY, LICENSES, MIGRATION, NOTICES, document, replace_pointer
from scripts.quality.product_version import require_next_version

MATRIX = "docs/governance/product-identity-consumer-matrix.json"
PINS = r'(?m)^APPROVED_(ADAPTATION|ADDITION)_FINGERPRINT = "[0-9a-f]{64}"$'


def snapshot(repo: Path, revision: str, path: str) -> bytes:
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("metadata comparison requires an exact commit")
    entry = subprocess.check_output(["git", "ls-tree", revision, "--", path], cwd=repo,
                                    stderr=subprocess.PIPE, timeout=15)
    header, actual = entry.strip().split(b"\t")
    mode, kind, blob = header.split()
    if mode != b"100644" or kind != b"blob" or actual.decode() != path:
        raise ValueError("metadata is not a regular file")
    size = int(subprocess.check_output(["git", "cat-file", "-s", blob.decode()], cwd=repo,
                                       stderr=subprocess.PIPE, timeout=15))
    if size > 32 * 1024 * 1024:
        raise ValueError("metadata snapshot exceeds the bounded input")
    return subprocess.check_output(["git", "cat-file", "blob", blob.decode()], cwd=repo,
                                    stderr=subprocess.PIPE, timeout=15)


def metadata_paths(repo: Path, base: str, head: str) -> set[str]:
    """Only ignore a complete, exact version-only projection/provenance delta.

    This is selection, not acceptance: counter/identity and fresh frontend
    provenance validation remain required. Any uncertainty retains old scope.
    """
    try:
        before = snapshot(repo, base, MATRIX)
        if before != snapshot(repo, head, MATRIX):
            return set()
        projected = {IDENTITY: ["/version"]}
        for entry in document(before)["product_version_projections"]:
            if entry["kind"] == "json-pointer":
                projected.setdefault(entry["path"], []).append(entry["pointer"])
        old = document(snapshot(repo, base, IDENTITY))["version"]
        new = document(snapshot(repo, head, IDENTITY))["version"]
        require_next_version(old, new)
        proposals = {path: snapshot(repo, head, path) for path in projected}
        for path, pointers in projected.items():
            expected = document(snapshot(repo, base, path))
            for pointer in pointers:
                replace_pointer(expected, pointer, old, new)
            if expected != document(proposals[path]):
                return set()
        migration = document(snapshot(repo, base, MIGRATION))
        actual = document(snapshot(repo, head, MIGRATION))
        for category, hash_key in (("adaptations", "targetSHA256"), ("additions", "sha256")):
            for record in migration[category]:
                raw = proposals.get("frontend/" + record["path"])
                if raw is not None:
                    record[hash_key] = hashlib.sha256(raw).hexdigest()
                    record["bytes"] = len(raw)
        for key in ("adaptationFingerprintSHA256", "additionFingerprintSHA256"):
            migration.pop(key)
            actual.pop(key)
        if migration != actual:
            return set()
        audit_before = snapshot(repo, base, AUDIT).decode()
        audit_after = snapshot(repo, head, AUDIT).decode()
        if (len(re.findall(PINS, audit_before)) != 2 or len(re.findall(PINS, audit_after)) != 2
                or re.sub(PINS, "REVIEWED_PIN", audit_before) != re.sub(PINS, "REVIEWED_PIN", audit_after)):
            return set()
        licenses = document(snapshot(repo, base, LICENSES))
        licenses["lockfileSHA256"] = hashlib.sha256(proposals["frontend/package-lock.json"]).hexdigest()
        for link in licenses["workspaceLinks"]:
            if "/version" in projected.get("frontend/" + link["path"] + "/package.json", []):
                if link["version"] != old:
                    return set()
                link["version"] = new
        if licenses != document(snapshot(repo, head, LICENSES)):
            return set()
        notice = snapshot(repo, base, NOTICES)
        previous_lock = hashlib.sha256(snapshot(repo, base, 'frontend/package-lock.json')).hexdigest()
        binding = ('Package lock SHA-256: ' + previous_lock).encode()
        if notice.count(binding) != 1:
            return set()
        expected_notice = notice.replace(binding, ('Package lock SHA-256: ' + licenses['lockfileSHA256']).encode())
        if expected_notice != snapshot(repo, head, NOTICES):
            return set()
        return set(projected) | {AUDIT, MIGRATION, LICENSES, NOTICES}
    except (OSError, ValueError, KeyError, TypeError, UnicodeError, subprocess.SubprocessError):
        return set()


def filter_paths(repo: Path, base: str, head: str, paths: list[str]) -> tuple[list[str], list[str]]:
    ignored = metadata_paths(repo, base, head) if IDENTITY in paths else set()
    if IDENTITY in paths and IDENTITY not in ignored and identity_version_only(repo, base, head):
        # A dependency/UI edit may legitimately change the rest of the metadata.
        # Exempt only this independently proved file; every other path keeps its
        # existing dependency, frontend, tooling or unknown-runtime coverage.
        ignored.add(IDENTITY)
    return [path for path in paths if path not in ignored], sorted(set(paths) & ignored)


def identity_version_only(repo: Path, base: str, head: str) -> bool:
    """Prove a single counter-field delta without trusting candidate code.

    This does not approve projections, provenance or tests. Their mandatory
    checks remain separate, and changed/unknown authority fails conservatively.
    """
    try:
        if snapshot(repo, base, MATRIX) != snapshot(repo, head, MATRIX):
            return False
        before = document(snapshot(repo, base, IDENTITY))
        after = document(snapshot(repo, head, IDENTITY))
        require_next_version(before['version'], after['version'])
        replace_pointer(before, '/version', before['version'], after['version'])
        return before == after
    except (OSError, ValueError, KeyError, TypeError, UnicodeError, subprocess.SubprocessError):
        return False
