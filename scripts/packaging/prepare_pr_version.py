"""Prepare version metadata on a clean task branch; never commit, push, merge or publish."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.quality.product_version import next_version
if __package__:
    from . import version_provenance as provenance
    from .pr_version_gate import read_version
else:
    import version_provenance as provenance
    from pr_version_gate import read_version


@dataclass(frozen=True)
class VersionPreparation:
    head: str
    base: str
    version: str
    originals: dict[str, bytes]
    outputs: dict[str, bytes]


def git(root: Path, *arguments: str) -> str:
    return subprocess.check_output(["git", *arguments], cwd=root, text=True, stderr=subprocess.PIPE, timeout=15).strip()


def check_owner(root: Path, head: str) -> None:
    if git(root, "rev-parse", "HEAD") != head:
        raise ValueError("candidate head changed")
    if git(root, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ValueError("version preparation requires a clean task worktree")
    branch = git(root, "branch", "--show-current")
    if not branch or branch == "main":
        raise ValueError("version preparation requires a task branch, never main")


def regular_target(root: Path, relative: str) -> Path:
    path = Path(relative)
    if path.is_absolute() or any(part in {"..", "."} for part in path.parts):
        raise ValueError("version projection path is not repository-relative")
    target = root / relative
    resolved = target.resolve()
    metadata = target.lstat()
    if (resolved != target or not resolved.is_relative_to(root.resolve())
            or not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1):
        raise ValueError("version projection must stay inside the task worktree as a regular file")
    return target


def atomic_write(target: Path, raw: bytes) -> None:
    """Preserve file mode and never leave a partially truncated JSON file."""
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=target.parent, prefix=f".{target.name}.version-", delete=False) as handle:
            temporary = Path(handle.name)
            handle.write(raw)
            handle.flush()
            os.fsync(handle.fileno())
        temporary.chmod(stat.S_IMODE(target.stat().st_mode))
        os.replace(temporary, target)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def plan(root: Path, base: str, head: str) -> VersionPreparation:
    check_owner(root, head)
    base_version = read_version(root, base)
    current = read_version(root, head)
    ancestry = subprocess.run(["git", "merge-base", "--is-ancestor", base, head], cwd=root,
                              capture_output=True, timeout=15, check=False)
    if ancestry.returncode:
        raise ValueError("update the task branch from current main before version preparation")
    expected = next_version(base_version)
    if current not in {base_version, expected}:
        raise ValueError("candidate contains an unexpected version change")
    proposed = {}
    originals = {}
    for relative, pointers in provenance.projections(root).items():
        target = regular_target(root, relative)
        raw = target.read_bytes()
        originals[relative] = raw
        value = provenance.document(raw)
        for pointer in pointers:
            provenance.replace_pointer(value, pointer, current, expected)
        proposed[relative] = (json.dumps(value, indent=2, ensure_ascii=False) + "\n").encode()
    if current == expected:
        # Same base and prepared head: validate projections, do not bump again.
        provenance.reviewed_audit(root)
        return VersionPreparation(head, base, expected, {}, {})
    # Validate every derived destination before reading or replacing it.
    for relative in provenance.DERIVED:
        regular_target(root, relative)
    outputs = dict(proposed)
    outputs.update(provenance.plan(root, proposed, set(proposed)))
    for relative in outputs:
        originals.setdefault(relative, regular_target(root, relative).read_bytes())
    outputs = {relative: raw for relative, raw in outputs.items() if raw != originals[relative]}
    return VersionPreparation(head, base, expected, originals, outputs)


def apply(root: Path, prepared: VersionPreparation) -> None:
    check_owner(root, prepared.head)
    if next_version(read_version(root, prepared.base)) != prepared.version:
        raise ValueError("version base changed")
    for relative, raw in prepared.originals.items():
        if regular_target(root, relative).read_bytes() != raw:
            raise ValueError("version projection changed after planning")
    written = []
    try:
        for relative, raw in prepared.outputs.items():
            written.append(relative)
            atomic_write(regular_target(root, relative), raw)
    except OSError:
        for relative in reversed(written):
            atomic_write(regular_target(root, relative), prepared.originals[relative])
        raise


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    root = args.repo.resolve()
    try:
        prepared = plan(root, args.base, args.head)
        if args.apply:
            apply(root, prepared)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print(json.dumps({"result": "FAIL", "reason": str(error)}))
        return 1
    print(json.dumps({"result": "PREPARED" if args.apply else "PLAN", "head": prepared.head,
                      "base": prepared.base, "product_version": prepared.version,
                      "changed_paths": sorted(prepared.outputs), "source_committed": False,
                      "remote_written": False, "release_published": False}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
