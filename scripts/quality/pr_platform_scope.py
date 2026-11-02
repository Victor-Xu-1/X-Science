"""Conservative filename-only Go build-context exclusion proofs.

Go itself decides filename constraints in a tiny same-context package. We never
infer exclusion from an OS suffix table or from IgnoredGoFiles alone. Active
Go declarations must be unchanged across both commits, so their embed patterns
remain current; deleted/unknown/module/dirty inputs retain normal coverage.
"""
from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile

_AUTHORITY = {"go.mod", "go.sum", "go.work", "go.work.sum"}
_CONTEXT = ("GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOWORK")


def _run(repo: Path, *args: str) -> str:
    result = subprocess.run(args, cwd=repo, text=True, capture_output=True,
                            check=False, timeout=30)
    if result.returncode:
        raise ValueError("cannot prove Go filename scope")
    return result.stdout


def _tree(repo: Path, revision: str) -> dict[str, tuple[str, str, str]]:
    if not re.fullmatch(r"[0-9a-f]{40,64}", revision) or set(revision) == {"0"}:
        raise ValueError("filename proof requires immutable existing commits")
    result = {}
    for record in _run(repo, "git", "ls-tree", "-r", "-z", revision).split("\0"):
        if not record:
            continue
        identity, path = record.split("\t", 1)
        mode, kind, oid = identity.split(" ")
        result[path] = (mode, kind, oid)
    return result


def _regular(value: tuple[str, str, str] | None) -> bool:
    return value is not None and value[0] in {"100644", "100755"} and value[1] == "blob"


def _context(repo: Path) -> dict[str, str]:
    value = json.loads(_run(repo, "go", "env", "-json", *_CONTEXT))
    if (not isinstance(value, dict) or set(value) != set(_CONTEXT)
            or any(not isinstance(item, str) for item in value.values())
            or value["GOWORK"] not in {"", "off"}
            or not re.fullmatch(r"go1\.[0-9]+(?:\.[0-9]+)?", value["GOVERSION"])):
        raise ValueError("unknown Go build context")
    return value


def _filename_ignored(names: set[str], context: dict[str, str]) -> set[str]:
    with tempfile.TemporaryDirectory(prefix="x-science-go-filename-proof-") as directory:
        probe = Path(directory)
        (probe / "go.mod").write_text(
            "module example.invalid/scopeproof\n\ngo " + context["GOVERSION"][2:] + "\n",
            encoding="utf-8")
        (probe / "proof.go").write_text("package proof\n", encoding="utf-8")
        for name in names:
            (probe / name).write_text("package proof\n", encoding="utf-8")
        # Match the effective toolchain, target, custom tags and cgo settings.
        # Values are compared internally, never printed (GOFLAGS can be private).
        if _context(probe) != context:
            raise ValueError("Go filename probe context differs")
        entry = json.loads(_run(probe, "go", "list", "-buildvcs=false", "-json", "."))
        if (not isinstance(entry, dict) or Path(entry.get("Dir", "")).resolve() != probe.resolve()
                or entry.get("Error") or not isinstance(entry.get("IgnoredGoFiles", []), list)):
            raise ValueError("unavailable Go filename proof")
        return names.intersection(entry.get("IgnoredGoFiles", []))


def _embedded(repo: Path, entries: list[dict], changed: set[str]) -> set[str]:
    # Default go list expands production embeds, but test variants carry the
    # expansion of TestEmbedPatterns/XTestEmbedPatterns in their EmbedFiles.
    names = {entry["ImportPath"] for entry in entries
             if (entry.get("TestEmbedPatterns") or entry.get("XTestEmbedPatterns"))
             and any(PurePosixPath(path).is_relative_to(Path(entry["Dir"]).relative_to(repo).as_posix())
                     for path in changed)}
    expanded = list(entries)
    if names:
        raw = _run(repo, "go", "list", "-buildvcs=false", "-test", "-json", *sorted(names))
        decoder, offset = json.JSONDecoder(), 0
        while raw[offset:].strip():
            offset += len(raw[offset:]) - len(raw[offset:].lstrip())
            entry, offset = decoder.raw_decode(raw, offset)
            if not isinstance(entry, dict):
                raise ValueError("invalid Go test embed metadata")
            expanded.append(entry)
    return {(Path(entry["Dir"]).relative_to(repo) / name).as_posix()
            for entry in expanded if Path(entry.get("Dir", "")).is_relative_to(repo)
            for field in ("EmbedFiles", "TestEmbedFiles", "XTestEmbedFiles")
            for name in entry.get(field, [])}


def filename_exclusions(repo: Path, base: str, head: str, paths: list[str],
                        entries: list[dict]) -> set[str]:
    """Return only same-context filename exclusions; every uncertainty returns no proof.

    This is an optimization, never a bypass flag. Declared runtime/verification
    consumers and actual embeds are also retained by select_packages itself.
    """
    changed = {path for path in paths if path.endswith(".go")}
    if not changed or any("\\" in path or ".." in PurePosixPath(path).parts
                          or PurePosixPath(path).is_absolute() for path in changed):
        return set()
    local = [entry for entry in entries if Path(entry.get("Dir", "")).is_relative_to(repo)]
    ignored = {(Path(entry["Dir"]).relative_to(repo) / name).as_posix()
               for entry in local for name in entry.get("IgnoredGoFiles", [])}
    if not changed.issubset(ignored):
        return set()
    try:
        # Current Go metadata must describe the exact head whose embeds we prove.
        if _run(repo, "git", "rev-parse", "HEAD").strip() != head:
            return set()
        # A dirty active declaration could invalidate the two-commit embed proof.
        if _run(repo, "git", "status", "--porcelain", "--untracked-files=normal", "--", "*.go",
                "go.mod", "go.sum", "go.work", "go.work.sum").strip():
            return set()
        before, after = _tree(repo, base), _tree(repo, head)
        difference = {path for path in before.keys() | after.keys() if before.get(path) != after.get(path)}
        if any(PurePosixPath(path).name in _AUTHORITY for path in difference):
            return set()
        go_changes = {path for path in difference if path.endswith(".go")}
        if not go_changes or not go_changes.issubset(ignored):
            return set()
        # Deletions/renames and nonregular preimages retain the old conservative scope.
        if any(not _regular(after.get(path)) or before.get(path) is not None and not _regular(before[path])
               for path in go_changes):
            return set()
        # Generated/untracked active declarations cannot prove unchanged embed patterns.
        for entry in local:
            for field in ("GoFiles", "CgoFiles", "TestGoFiles", "XTestGoFiles"):
                if any(not _regular(after.get((Path(entry["Dir"]).relative_to(repo) / name).as_posix()))
                       for name in entry.get(field, [])):
                    return set()
        names = {PurePosixPath(path).name for path in go_changes}
        excluded = _filename_ignored(names, _context(repo))
        if not names.issubset(excluded):
            return set()
        # Every changed Go declaration is filename-inactive on both sides. No
        # active embed directive changed; current embed membership is sufficient.
        return changed.difference(_embedded(repo, local, changed))
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        return set()
