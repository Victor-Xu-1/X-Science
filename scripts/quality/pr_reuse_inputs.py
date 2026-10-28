"""Bounded execution inputs for the existing affected-Go test receipt."""

from __future__ import annotations

import hashlib
import importlib.metadata
import json
from pathlib import Path
import shlex
import shutil
import subprocess
import sys

if __package__:
    from . import runtime_test_inventory
else:
    import runtime_test_inventory

SOURCE_INPUTS = (
    "go.mod", "go.sum", "assets/optional/mcp-servers/bio-tools/requirements-test.txt",
    ".github/workflows/quality-pr.yml", ".github/workflows/quality-main.yml",
    "scripts/quality/pr_fast_scope.py", "scripts/quality/pr_go_reuse.py",
    "scripts/quality/pr_reuse_github.py", "scripts/quality/pr_reuse_inputs.py",
    "scripts/quality/pr_test_partition.py", "scripts/quality/pr_test_costs.json",
    "scripts/quality/runtime_test_inventory.py", "scripts/quality/runtime_test_shards.py",
    "scripts/quality/runtime_input_scope.py", "scripts/quality/runtime_input_scope.json",
    "scripts/quality/verification_scope.py", "scripts/quality/verification_scope.json",
    "scripts/quality/pr_metadata_scope.py", "scripts/quality/pr_namespace_scope.py",
    "scripts/quality/go_namespace_equivalence.go",
    "scripts/quality/pr_dependency_scope.py", "scripts/quality/pr_build_scope.py",
)


def command(*args: str) -> str:
    result = subprocess.run(args, capture_output=True, text=True, check=False, timeout=15)
    if result.returncode:
        raise ValueError("relevant test tool is unavailable")
    return (result.stdout or result.stderr).strip().splitlines()[0]


def python_dependencies(requirements: Path) -> dict[str, str]:
    # Resolve only the test requirements and their active dependencies. An
    # unrelated globally installed distribution is not a reuse prerequisite.
    from packaging.requirements import Requirement
    from packaging.utils import canonicalize_name

    pending = [(Requirement(line.strip()), frozenset()) for line in requirements.read_text(encoding="utf-8").splitlines()
               if line.strip() and not line.lstrip().startswith("#")]
    seen: set[tuple[str, tuple[str, ...]]] = set()
    versions: dict[str, str] = {}
    while pending:
        requirement, parents = pending.pop()
        if requirement.marker and not any(requirement.marker.evaluate({"extra": extra})
                                          for extra in (parents or frozenset({""}))):
            continue
        name = canonicalize_name(requirement.name)
        extras = tuple(sorted(requirement.extras))
        if (name, extras) in seen:
            continue
        seen.add((name, extras))
        distribution = importlib.metadata.distribution(name)
        version = distribution.version
        if requirement.specifier and not requirement.specifier.contains(version, prereleases=True):
            raise ValueError("resolved test dependency does not satisfy its requirement")
        if name in versions and versions[name] != version:
            raise ValueError("inconsistent resolved test dependency")
        versions[name] = version
        for dependency in distribution.requires or []:
            pending.append((Requirement(dependency), frozenset(extras)))
        if len(seen) > 1000:
            raise ValueError("test dependency closure is too large")
    return dict(sorted(versions.items()))


def capture(repo: Path) -> dict:
    requirement = repo / "assets/optional/mcp-servers/bio-tools/requirements-test.txt"
    go_fields = ("GOVERSION", "GOOS", "GOARCH", "GOAMD64", "GOEXPERIMENT", "CGO_ENABLED", "GOFLAGS", "GOTOOLCHAIN",
                 "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS")
    result = subprocess.run(["go", "env", "-json", *go_fields],
                            cwd=repo, capture_output=True, text=True, check=True, timeout=15)
    go = json.loads(result.stdout)
    expected = set(go_fields)
    if not isinstance(go, dict) or set(go) != expected or any(not isinstance(v, str) for v in go.values()):
        raise ValueError("invalid Go test environment")
    if go["CGO_ENABLED"] == "1":
        for field in ("CC", "CXX"):
            compiler = shlex.split(go[field])
            if len(compiler) != 1:
                raise ValueError("compiler identity cannot be established")
            go[field + "_version"] = command(compiler[0], "--version")
    for field in ("CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"):
        go[field] = "sha256:" + hashlib.sha256(go[field].encode()).hexdigest()
    probe = runtime_test_inventory.execution_plan("pr", 0, False, {"fixture": ["TestFixture"]},
                                                  {"fixture": ["TestFixture"]}, batch_size=64)
    command_args = probe["commands"][0]
    if "-run" not in command_args:
        raise ValueError("test executor command is not bounded")
    return {
        "go": go,
        "python": ".".join(map(str, sys.version_info[:3])),
        "python_dependencies": python_dependencies(requirement),
        "node": command("node", "--version") if shutil.which("node") is not None else None,
        "bwrap": command("bwrap", "--version"),
        "pdfinfo": command("pdfinfo", "-v"),
        "source_sha256": {path: hashlib.sha256((repo / path).read_bytes()).hexdigest() for path in SOURCE_INPUTS},
        "discovery_flags": ["go", "test", "-buildvcs=false", "-json", "-p", "1", "-list", "."],
        "test_flags": command_args[:command_args.index("-run")],
    }
