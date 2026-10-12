"""Deterministic cost-aware PR partitions preserve the complete Go inventory."""
from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field
import hashlib
import json
import os
from pathlib import Path
import re
import stat

if __package__:
    from .runtime_test_inventory import TEST_NAME
else:
    from runtime_test_inventory import TEST_NAME

MAX_SHARDS = 4
BATCH_SIZE = 64
DEFAULT_TEST_MS = 1000
DEFAULT_BATCH_MS = 2200
MAX_COST_MS = 1_800_000
MAX_PROFILE_BYTES = 65536
PROFILE_PATH = "scripts/quality/pr_test_costs.json"
PROFILE_SCHEMA = "x-science.pr-test-costs.v1"
PLAN_SCHEMA = "x-science.pr-test-partition.v1"
ALGORITHM = "global-top-level-lpt-batch-v1"
PACKAGE_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._~-]*(?:/[A-Za-z0-9_][A-Za-z0-9._~-]*)*")
ENVIRONMENT_FIELDS = {"go_version", "goos", "goarch", "race", "batch_size"}


def digest(value: object) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"),
                                     ensure_ascii=True).encode()).hexdigest()


def validate_partition(index: int, count: int) -> None:
    if type(count) is not int or type(index) is not int or not 1 <= count <= MAX_SHARDS or not 0 <= index < count:
        raise ValueError("invalid affected-test partition")


def job_matrix(packages: list[str]) -> dict:
    count = min(MAX_SHARDS, max(1, len(packages)))
    return {"include": [{"index": index, "count": count} for index in range(count)]}


def canonical_inventory(inventory: dict[str, list[str]]) -> dict[str, list[str]]:
    if not isinstance(inventory, dict):
        raise ValueError("affected-test inventory must be a package map")
    for package, names in inventory.items():
        if not isinstance(package, str) or not PACKAGE_NAME.fullmatch(package) or not isinstance(names, list):
            raise ValueError("invalid affected-test package inventory")
        if any(not isinstance(name, str) or not TEST_NAME.fullmatch(name) for name in names):
            raise ValueError("invalid affected-test top-level name")
        if len(names) != len(set(names)):
            raise ValueError("duplicate affected-test inventory name")
    return {package: sorted(inventory[package]) for package in sorted(inventory)}


@dataclass(frozen=True)
class CostProfile:
    anchor_epoch: int = 0
    status: str = "fallback"
    reason: str = "missing profile"
    sha256: str | None = None
    observed_at_epoch: int | None = None
    valid_until_epoch: int | None = None
    environment: dict = field(default_factory=dict)
    default_test_ms: int = DEFAULT_TEST_MS
    default_batch_ms: int = DEFAULT_BATCH_MS
    costs: dict[tuple[str, str], int] = field(default_factory=dict)

    def metadata(self) -> dict:
        return {"sha256": self.sha256, "status": self.status, "reason": self.reason,
                "anchor_epoch": self.anchor_epoch, "observed_at_epoch": self.observed_at_epoch,
                "valid_until_epoch": self.valid_until_epoch, "environment": self.environment,
                "default_test_ms": self.default_test_ms, "default_batch_ms": self.default_batch_ms}


def _integer(value: object, label: str, lower: int, upper: int) -> int:
    if type(value) is not int or not lower <= value <= upper:
        raise ValueError(f"invalid cost profile {label}: expected integer in [{lower}, {upper}]")
    return value


def _environment(value: object) -> dict:
    if (not isinstance(value, dict) or set(value) != ENVIRONMENT_FIELDS
            or not isinstance(value["go_version"], str)
            or not re.fullmatch(r"go[0-9]+\.[0-9]+(?:\.[0-9]+)?", value["go_version"])
            or any(not isinstance(value[key], str) or not re.fullmatch(r"[a-z0-9]+", value[key])
                   for key in ("goos", "goarch")) or type(value["race"]) is not bool):
        raise ValueError("invalid cost profile environment")
    _integer(value["batch_size"], "batch_size", 1, BATCH_SIZE)
    return value


def _unique_object(pairs: list[tuple[str, object]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate cost profile JSON key: {key}")
        result[key] = value
    return result


def _reject_constant(item: str) -> None:
    raise ValueError(f"nonfinite cost profile number: {item}")


def load_profile(path: Path, environment: dict, anchor_epoch: int) -> CostProfile:
    """Missing/stale/incompatible data falls back; malformed data always fails."""
    _integer(anchor_epoch, "anchor_epoch", 0, 253402300799)
    _environment(environment)
    # Reject symlink files and ancestors rather than following a profile elsewhere.
    if ".." in path.parts:
        raise ValueError("cost profile path must not contain traversal")
    path = path.absolute()
    if any(parent.is_symlink() for parent in (path, *path.parents)):
        raise ValueError("cost profile path must not contain symlinks")
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NONBLOCK", 0) | getattr(os, "O_NOFOLLOW", 0))
    except FileNotFoundError:
        return CostProfile(anchor_epoch=anchor_epoch, environment=environment)
    with os.fdopen(descriptor, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError("cost profile must be a regular file")
        data = stream.read(MAX_PROFILE_BYTES + 1)
    if len(data) > MAX_PROFILE_BYTES:
        raise ValueError("cost profile exceeds 64KiB")
    sha256 = hashlib.sha256(data).hexdigest()
    try:
        value = json.loads(data.decode("utf-8"), object_pairs_hook=_unique_object,
                           parse_constant=_reject_constant)
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError) as error:
        raise ValueError("invalid cost profile JSON") from error
    fields = {"schema", "environment", "observed_at_epoch", "valid_until_epoch",
              "default_test_ms", "default_batch_ms", "costs"}
    if not isinstance(value, dict) or set(value) != fields or value["schema"] != PROFILE_SCHEMA:
        raise ValueError("invalid cost profile schema")
    profile_environment = _environment(value["environment"])
    observed = _integer(value["observed_at_epoch"], "observed_at_epoch", 0, 253402300799)
    expires = _integer(value["valid_until_epoch"], "valid_until_epoch", observed + 1, observed + 30 * 86400)
    default = _integer(value["default_test_ms"], "default_test_ms", 1, MAX_COST_MS)
    batch = _integer(value["default_batch_ms"], "default_batch_ms", 1, MAX_COST_MS)
    if not isinstance(value["costs"], dict):
        raise ValueError("cost profile costs must be a package map")
    costs = {}
    for package, tests in value["costs"].items():
        if not isinstance(package, str) or not PACKAGE_NAME.fullmatch(package) or not isinstance(tests, dict) or not tests:
            raise ValueError("invalid cost profile package")
        for name, cost in tests.items():
            if not TEST_NAME.fullmatch(name):
                raise ValueError("invalid cost profile top-level test")
            costs[package, name] = _integer(cost, "test cost_ms", 1, MAX_COST_MS)
    reason = ("environment mismatch" if profile_environment != environment else
              "anchor precedes observations" if anchor_epoch < observed else
              "expired profile" if anchor_epoch >= expires else "compatible profile")
    active = reason == "compatible profile"
    return CostProfile(anchor_epoch, "active" if active else "fallback", reason, sha256,
                       observed, expires, environment, default if active else DEFAULT_TEST_MS,
                       batch if active else DEFAULT_BATCH_MS, costs if active else {})


def validate_assignment(inventory: dict[str, list[str]], shards: list[dict[str, list[str]]], count: int) -> None:
    """Check complete union, disjoint tests and exactly-once compile-only packages."""
    validate_partition(0, count)
    mandatory = canonical_inventory(inventory)
    if not isinstance(shards, list) or len(shards) != count:
        raise ValueError("invalid affected-test assignment count")
    observed: Counter = Counter()
    compile_only: Counter = Counter()
    for shard in shards:
        canonical_inventory(shard)
        for package, tests in shard.items():
            if package not in mandatory or (not tests and mandatory[package]):
                raise ValueError("unexpected affected-test assignment package")
            if not tests:
                compile_only[package] += 1
            observed.update((package, test) for test in tests)
    expected = Counter((package, test) for package, tests in mandatory.items() for test in tests)
    empty = Counter({package: 1 for package, tests in mandatory.items() if not tests})
    if observed != expected or compile_only != empty:
        raise ValueError("affected-test assignment is incomplete or overlapping")


def assignment(inventory: dict[str, list[str]], count: int, profile: CostProfile | None = None) -> dict:
    validate_partition(0, count)
    mandatory = canonical_inventory(inventory)
    profile = profile or CostProfile()
    shards: list[dict[str, list[str]]] = [{} for _ in range(count)]
    loads = [0] * count
    tests = [(package, name) for package, names in mandatory.items() for name in names]
    weights = {identity: profile.costs.get(identity, profile.default_test_ms) for identity in tests}
    for weight in [*weights.values(), profile.default_test_ms, profile.default_batch_ms]:
        _integer(weight, "assignment cost_ms", 1, MAX_COST_MS)
    for package, name in sorted(tests, key=lambda item: (-weights[item], *item)):
        scores = [loads[index] + weights[package, name] + (
            profile.default_batch_ms if len(shards[index].get(package, [])) % BATCH_SIZE == 0 else 0)
                  for index in range(count)]
        index = min(range(count), key=lambda candidate: (scores[candidate], candidate))
        loads[index] = scores[index]
        shards[index].setdefault(package, []).append(name)
    for package, names in mandatory.items():
        if not names:
            index = int.from_bytes(hashlib.sha256(package.encode()).digest()[:4], "big") % count
            shards[index][package] = []
    shards = [canonical_inventory(shard) for shard in shards]
    validate_assignment(mandatory, shards, count)
    hits = set(tests).intersection(profile.costs)
    return {"shards": shards, "metadata": {"schema": PLAN_SCHEMA, "algorithm": ALGORITHM,
            "shard_count": count, "batch_size": BATCH_SIZE,
            "inventory_sha256": digest(mandatory), "assignment_sha256": digest(shards),
            "estimated_load_ms": loads, "profile": profile.metadata(),
            "profiled_test_count": len(hits), "default_test_count": len(tests) - len(hits),
            "unused_profile_test_count": len(profile.costs) - len(hits)}}


def select(inventory: dict[str, list[str]], index: int, count: int,
           profile: CostProfile | None = None) -> dict[str, list[str]]:
    validate_partition(index, count)
    return assignment(inventory, count, profile)["shards"][index]
