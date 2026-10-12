"""Derive one candidate's version-only provenance without approving source changes."""

from __future__ import annotations

import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import stat
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.quality.product_version import require_next_version

AUDIT = "scripts/audit/audit_frontend_migration.py"
AUDIT_HELPER = "scripts/audit/frontend_source_namespace.py"
MIGRATION = "frontend/MIGRATION_MANIFEST.json"
LICENSES = "frontend/THIRD_PARTY_LICENSES.json"
NOTICES = "docs/licenses/frontend-bundle/NOTICE.txt"
DERIVED = {AUDIT, MIGRATION, LICENSES, NOTICES}
IDENTITY = "product-identity.json"


def document(raw: bytes) -> dict:
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = value
        return result
    value = json.loads(raw, object_pairs_hook=unique)
    if not isinstance(value, dict):
        raise ValueError("expected a JSON object")
    return value


def projections(root: Path) -> dict[str, list[str]]:
    matrix = document((root / "docs/governance/product-identity-consumer-matrix.json").read_bytes())
    result = {IDENTITY: ["/version"]}
    for entry in matrix["product_version_projections"]:
        if entry["kind"] == "json-pointer":
            result.setdefault(entry["path"], []).append(entry["pointer"])
    return result


def replace_pointer(value: dict, pointer: str, old: str, new: str) -> None:
    keys = [key.replace("~1", "/").replace("~0", "~") for key in pointer.split("/")[1:]]
    target = value
    for key in keys[:-1]:
        target = target[key]
    if target[keys[-1]] != old:
        raise ValueError("base version projection is not aligned")
    target[keys[-1]] = new


def require_notice_binding(root: Path) -> tuple[bytes, str]:
    """Validate the existing notice before deriving a version-only digest change."""
    path = root / NOTICES
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 4 * 1024 * 1024:
        raise ValueError("frontend notice must be a bounded regular file")
    raw = path.read_bytes()
    recorded = re.findall(rb'(?m)^Package lock SHA-256: ([0-9a-f]{64})$', raw)
    digest = hashlib.sha256((root / 'frontend/package-lock.json').read_bytes()).hexdigest()
    if len(recorded) != 1 or recorded[0].decode() != digest:
        raise ValueError("frontend notice is not bound to the current dependency lock")
    return raw, digest


def reviewed_audit(root: Path):
    """Execute only the tooling checkout's auditor; candidate code is data."""
    tooling = Path(__file__).resolve().parents[2]
    trusted = regular_audit_input(tooling, AUDIT)
    target = regular_audit_input(root, AUDIT)
    # A candidate cannot establish its own trust by invoking its local copy.
    # samefile also rejects symlink/hardlink aliases across distinct roots.
    if root.resolve() == tooling or target.samefile(trusted):
        raise ValueError("version preparation requires separate reviewed tooling")
    candidate = target.read_text()
    pattern = r'(?m)^APPROVED_(ADAPTATION|ADDITION|REMOVAL)_FINGERPRINT = "([0-9a-f]{64})"$'
    pins = dict(re.findall(pattern, candidate))
    if set(pins) != {"ADAPTATION", "ADDITION", "REMOVAL"}:
        raise ValueError("unexpected candidate audit fingerprint contract")
    normalize = lambda source: re.sub(pattern, lambda match: f'APPROVED_{match[1]}_FINGERPRINT = "REVIEWED"', source)
    trusted_source = trusted.read_text()
    if normalize(candidate) != normalize(trusted_source):
        raise ValueError("candidate audit implementation requires a reviewed tooling update")
    helper = regular_audit_input(tooling, AUDIT_HELPER)
    candidate_helper = regular_audit_input(root, AUDIT_HELPER)
    if candidate_helper.samefile(helper):
        raise ValueError("version preparation requires separate reviewed tooling helper")
    helper_source = helper.read_bytes()
    if candidate_helper.read_bytes() != helper_source:
        raise ValueError("candidate audit helper requires a reviewed tooling update")
    spec = importlib.util.spec_from_file_location("version_baseline_audit", trusted)
    audit = importlib.util.module_from_spec(spec)
    helper_spec = importlib.util.spec_from_file_location("frontend_source_namespace", helper)
    helper_module = importlib.util.module_from_spec(helper_spec)
    previous = sys.modules.get("frontend_source_namespace")
    previously_loaded = "frontend_source_namespace" in sys.modules
    try:
        # Import only this exact reviewed file. No candidate directory enters
        # sys.path, and an existing cached module cannot supply different code.
        exec(helper_spec.loader.source_to_code(helper_source, str(helper)), helper_module.__dict__)
        sys.modules["frontend_source_namespace"] = helper_module
        exec(spec.loader.source_to_code(trusted_source, str(trusted)), audit.__dict__)
    finally:
        if previously_loaded:
            sys.modules["frontend_source_namespace"] = previous
        else:
            sys.modules.pop("frontend_source_namespace", None)
    # Validate the candidate's existing recorded claims against fresh bytes,
    # not the tooling revision's old fingerprints or a previous test result.
    for kind, digest in pins.items():
        setattr(audit, f"APPROVED_{kind}_FINGERPRINT", digest)
    audit.main(["--root", str(root), "--check"])
    return audit


def regular_audit_input(root: Path, relative: str) -> Path:
    pure = Path(relative)
    if (pure.is_absolute() or pure.as_posix() != relative or "\\" in relative
            or any(part in {"", ".", ".."} for part in relative.split("/"))):
        raise ValueError("audit input path must be exact and contained")
    current = root
    for part in pure.parts:
        if current.is_symlink():
            raise ValueError("audit input requires separate reviewed tooling without symlinks")
        current = current / part
    metadata = current.lstat()
    if (current.is_symlink() or not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1
            or metadata.st_size > 4 * 1024 * 1024 or not current.resolve().is_relative_to(root.resolve())):
        raise ValueError("audit input requires bounded regular separate reviewed tooling")
    return current


def plan(root: Path, proposed: dict[str, bytes], changed: set[str]) -> dict[str, bytes]:
    """Check the complete PR delta, then derive only the three audit outputs."""
    projected = projections(root)
    if changed - (set(projected) | DERIVED):
        raise ValueError("version proposal contains non-version changes")
    old = document((root / IDENTITY).read_bytes())["version"]
    new = document(proposed[IDENTITY])["version"]
    require_next_version(old, new)
    for path, pointers in projected.items():
        expected = document((root / path).read_bytes())
        for pointer in pointers:
            replace_pointer(expected, pointer, old, new)
        if document(proposed[path]) != expected:
            raise ValueError(f"non-version JSON change: {path}")

    audit = reviewed_audit(root)
    notice, notice_digest = require_notice_binding(root)
    migration = document((root / MIGRATION).read_bytes())
    for category, hash_key in (("adaptations", "targetSHA256"), ("additions", "sha256")):
        for record in migration[category]:
            raw = proposed.get("frontend/" + record["path"])
            if raw is not None:
                record[hash_key] = hashlib.sha256(raw).hexdigest()
                record["bytes"] = len(raw)
    adaptation = audit.adaptation_fingerprint(migration["adaptations"])
    addition = audit.addition_fingerprint(migration["additions"])
    migration["adaptationFingerprintSHA256"] = adaptation
    migration["additionFingerprintSHA256"] = addition
    source = (root / AUDIT).read_text()
    for kind, digest in (("ADAPTATION", adaptation), ("ADDITION", addition)):
        source, count = re.subn(
            rf'(?m)^APPROVED_{kind}_FINGERPRINT = "[0-9a-f]{{64}}"$',
            f'APPROVED_{kind}_FINGERPRINT = "{digest}"', source,
        )
        if count != 1:
            raise ValueError("unexpected audit fingerprint contract")
    licenses = copy.deepcopy(document((root / LICENSES).read_bytes()))
    licenses["lockfileSHA256"] = hashlib.sha256(proposed["frontend/package-lock.json"]).hexdigest()
    for link in licenses["workspaceLinks"]:
        path = "frontend/" + link["path"] + "/package.json"
        if "/version" in projected.get(path, []):
            link["version"] = new
    next_notice = notice.replace(('Package lock SHA-256: ' + notice_digest).encode(),
                                 ('Package lock SHA-256: ' + licenses['lockfileSHA256']).encode())
    return {AUDIT: source.encode(), MIGRATION: audit.json_bytes(migration), LICENSES: audit.json_bytes(licenses),
            NOTICES: next_notice}
