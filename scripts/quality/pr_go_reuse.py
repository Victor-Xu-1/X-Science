"""Verify original PR test receipts on the existing main test path.

Reuse never changes the original execution SHA. Any unavailable or incomplete
evidence returns to the normal affected-test execution.
"""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import queue
import subprocess
import threading
import time

if __package__:
    from . import pr_reuse_inputs, pr_test_partition, runtime_test_inventory
else:
    import pr_reuse_inputs
    import pr_test_partition
    import runtime_test_inventory

RECEIPT_FILES = ("scope.json", "plan.json", "summary.json", "events.jsonl")
SCHEMA = "x-science.pr-go-execution-provenance.v1"
REUSE_BUDGET_SECONDS = 25


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def git(repo: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=repo, capture_output=True, text=True, check=True, timeout=15)
    return result.stdout.strip()


def event() -> dict:
    path = Path(os.environ["GITHUB_EVENT_PATH"])
    if path.stat().st_size > 4 * 1024 * 1024:
        raise ValueError("event is too large")
    result = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(result, dict):
        raise ValueError("invalid event")
    return result


def prepare_pr_inputs(repo: Path) -> dict | None:
    """Capture real inputs before PR execution; failure only disables reuse."""
    if os.getenv("GITHUB_ACTIONS") != "true" or os.getenv("GITHUB_EVENT_NAME") != "pull_request":
        return None
    try:
        return pr_reuse_inputs.capture(repo)
    except Exception:
        print("PR input capture unavailable; main will execute its original test path.", flush=True)
        return None


def write_pr_provenance(repo: Path, base: str, head: str, log_dir: Path, index: int, count: int,
                        initial_inputs: dict | None) -> None:
    """Add optional provenance only after the existing PR executor succeeds."""
    if os.getenv("GITHUB_ACTIONS") != "true" or os.getenv("GITHUB_EVENT_NAME") != "pull_request":
        return
    try:
        if initial_inputs is None or pr_reuse_inputs.capture(repo) != initial_inputs:
            raise ValueError("test inputs changed or were unavailable")
        value = event()
        pr = value["pull_request"]
        repository = value["repository"]
        if (pr["base"]["sha"] != base or os.environ["GITHUB_SHA"] != head
                or git(repo, "rev-parse", "HEAD") != head
                or git(repo, "status", "--porcelain", "--untracked-files=normal")
                or pr["base"]["repo"]["id"] != repository["id"]
                or pr["head"]["repo"]["id"] != repository["id"]):
            raise ValueError("PR execution identity is not eligible")
        files = {name: digest((log_dir / name).read_bytes()) for name in RECEIPT_FILES}
        record = {"schema": SCHEMA, "repository_id": repository["id"], "pr_number": pr["number"],
                  "base": base, "pr_head": pr["head"]["sha"], "execution_sha": head,
                  "tree": git(repo, "rev-parse", "HEAD^{tree}"),
                  "run_id": int(os.environ["GITHUB_RUN_ID"]),
                  "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
                  "job": os.environ["GITHUB_JOB"], "index": index, "count": count,
                  "inputs": initial_inputs, "files": files}
        (log_dir / "provenance.json").write_text(json.dumps(record, sort_keys=True) + "\n", encoding="utf-8")
    except Exception:
        # A successful normal PR test remains valid, but cannot be reused.
        print("PR provenance unavailable; main will execute its original test path.", flush=True)


def _json(files: dict, name: str) -> dict:
    value = json.loads(files[name])
    if not isinstance(value, dict):
        raise ValueError("invalid receipt JSON")
    return value


def certified_inventory(repo: Path, receipts: dict, packages: list[str], binding: dict,
                        profile: pr_test_partition.CostProfile, count: int) -> dict:
    """Reconstruct only a complete producer-sealed, same-profile inventory.

    This candidate is not accepted here: the original receipt validator still
    authenticates every input/file, complete summary and actual test event.
    Current tooling hashes exclude producers that captured after discovery.
    """
    pr_test_partition.validate_partition(0, count)
    if (profile.status != "active" or profile.anchor_epoch != binding.get("anchor_epoch")
            or profile.environment != binding.get("environment") or binding.get("source_state") != "clean"):
        raise ValueError("current planning profile is not eligible")
    artifacts = receipts["artifacts"]
    by_index = {item["index"]: item for item in artifacts}
    if len(artifacts) != count or len(by_index) != count or set(by_index) != set(range(count)):
        raise ValueError("PR partition count differs from current planning")
    selected, inventory = [], {}
    for index in range(count):
        shard = pr_test_partition.canonical_inventory(_json(by_index[index]["files"], "plan.json")["selected"])
        selected.append(shard)
        for package, names in shard.items():
            inventory.setdefault(package, []).extend(names)
    inventory = pr_test_partition.canonical_inventory(inventory)
    if sorted(inventory) != sorted(packages) or len(set(packages)) != len(packages):
        raise ValueError("sealed inventory does not cover current packages")
    pr_test_partition.validate_assignment(inventory, selected, count)
    first = _json(by_index[0]["files"], "plan.json")["partition"]
    anchor = first.get("anchor_epoch")
    if type(anchor) is not int or anchor < 0 or first.get("environment") != binding.get("environment"):
        raise ValueError("original immutable planning inputs are unavailable")
    original = pr_test_partition.load_profile(repo / pr_test_partition.PROFILE_PATH, profile.environment, anchor)
    current_metadata, original_metadata = profile.metadata(), original.metadata()
    # Anchors keep their real execution identities. Both must independently be
    # active under the exact same source profile, not straddle its validity edge.
    if (original.status != "active" or
            {key: value for key, value in current_metadata.items() if key != "anchor_epoch"} !=
            {key: value for key, value in original_metadata.items() if key != "anchor_epoch"}):
        raise ValueError("original and current planning profiles differ")
    expected = pr_test_partition.assignment(inventory, count, original)
    if selected != expected["shards"] or selected != pr_test_partition.assignment(inventory, count, profile)["shards"]:
        raise ValueError("sealed assignment differs from current planning")
    for index in range(count):
        plan = _json(by_index[index]["files"], "plan.json")
        partition = plan["partition"]
        if (plan.get("inventory_sha256") != pr_test_partition.digest(inventory)
                or partition.get("anchor_epoch") != anchor
                or partition.get("environment") != profile.environment
                or any(partition.get(key) != value for key, value in expected["metadata"].items())):
            raise ValueError("sealed inventory or original profile metadata differs")
    return inventory


def validate_receipts(receipts: dict, *, base: str, main: str, tree: str,
                      inventory: dict[str, list[str]], inputs: dict, paths: list[str]) -> dict:
    """Prove all original partitions cover the current discovered inventory."""
    run, pr, artifacts = receipts["run"], receipts["pr"], receipts["artifacts"]
    canonical = pr_test_partition.canonical_inventory(inventory)
    count = len(artifacts)
    if not 1 <= count <= pr_test_partition.MAX_SHARDS:
        raise ValueError("invalid PR partition count")
    by_index = {artifact["index"]: artifact for artifact in artifacts}
    if set(by_index) != set(range(count)) or len(by_index) != count:
        raise ValueError("missing or duplicate PR partition")
    assignments, references, execution = [], [], None
    for index in range(count):
        artifact = by_index[index]
        files = artifact["files"]
        provenance, plan, summary, scope = (_json(files, name) for name in
            ("provenance.json", "plan.json", "summary.json", "scope.json"))
        if (provenance.get("schema") != SCHEMA or provenance.get("repository_id") != receipts["repository"]["id"]
                or provenance.get("pr_number") != pr["number"] or provenance.get("base") != base
                or provenance.get("pr_head") != pr["head_sha"] or provenance.get("tree") != tree
                or provenance.get("run_id") != run["id"] or provenance.get("run_attempt") != run["attempt"]
                or provenance.get("index") != index or provenance.get("count") != count
                or provenance.get("job") != "pr-go-shards" or provenance.get("inputs") != inputs
                or provenance.get("files") != {name: digest(files[name]) for name in RECEIPT_FILES}):
            raise ValueError("PR provenance mismatch")
        original = provenance["execution_sha"]
        if execution is None:
            execution = original
        if original != execution:
            raise ValueError("PR execution identities disagree")
        partition = plan.get("partition", {})
        if (partition.get("base") != base or partition.get("head") != original
                or partition.get("checkout_head") != original or partition.get("source_state") != "clean"
                or partition.get("shard_index") != index or partition.get("shard_count") != count
                or scope.get("base") != base or scope.get("head") != original
                or scope.get("shard_index") != index or scope.get("shard_count") != count
                or scope.get("changed_paths") != paths or scope.get("packages") != sorted(canonical)
                or plan.get("inventory_sha256") != pr_test_partition.digest(canonical)
                or partition.get("inventory_sha256") != plan.get("inventory_sha256")):
            raise ValueError("PR plan does not match current inventory")
        selected = pr_test_partition.canonical_inventory(plan["selected"])
        expected_plan = runtime_test_inventory.execution_plan("pr", index, False, canonical, selected,
                                                              batch_size=pr_test_partition.BATCH_SIZE) if selected else {"commands": []}
        if plan.get("commands") != expected_plan["commands"]:
            raise ValueError("PR test commands do not match the selected inventory")
        if (summary.get("exit_code") != 0 or summary.get("unstarted_process_batches") != 0
                or any(summary.get(key) != [] for key in ("missing_tests", "unexpected_tests", "missing_packages", "duplicate_tests"))
                or summary.get("executed_top_level") != sum(map(len, selected.values()))
                or summary.get("batch_exit_codes") != [0] * len(plan["commands"])):
            raise ValueError("PR execution is incomplete")
        observed = {}
        for raw in files["events.jsonl"].splitlines():
            try:
                item = json.loads(raw)
            except json.JSONDecodeError:
                continue
            action, name = item.get("Action"), item.get("Test", "")
            if action in ("pass", "fail", "skip") and name and "/" not in name:
                key = (item.get("Package"), name)
                if key in observed or action == "fail":
                    raise ValueError("PR events contain a duplicate or failure")
                observed[key] = action
        expected = {(package, name) for package, names in selected.items() for name in names}
        if set(observed) != expected:
            raise ValueError("PR events do not cover planned tests")
        assignments.append(selected)
        references.append({"index": index, "artifact_id": artifact["id"], "digest": artifact["digest"]})
    pr_test_partition.validate_assignment(canonical, assignments, count)
    if run.get("execution_sha") != execution:
        raise ValueError("PR execution commit differs from verified commit")
    assignment_hash = pr_test_partition.digest(assignments)
    if any(_json(by_index[i]["files"], "plan.json")["partition"].get("assignment_sha256") != assignment_hash
           for i in range(count)):
        raise ValueError("PR assignments do not agree")
    return {"schema": "x-science.main-go-reuse.v1", "status": "verified-reuse", "main_sha": main,
            "repository_id": receipts["repository"]["id"],
            "base_sha": base, "tree": tree, "pr": pr["number"], "pr_head": pr["head_sha"],
            "execution_sha": execution, "run_id": run["id"], "run_attempt": run["attempt"],
            "workflow_id": run["workflow_id"], "checks": receipts["checks"], "jobs": receipts["jobs"],
            "inventory_sha256": pr_test_partition.digest(canonical),
            "assignment_sha256": assignment_hash, "artifacts": references}


def _bounded(operation, timeout: float):
    """Return promptly even if a network resolver ignores its socket timeout.

    The worker only reads and computes; it cannot write an acceptance record.
    A late response is discarded and the original test executor proceeds.
    """
    outcomes = queue.Queue(maxsize=1)

    def run():
        try:
            outcomes.put((True, operation()))
        except Exception as error:
            outcomes.put((False, error))

    threading.Thread(target=run, name="pr-go-reuse-evidence", daemon=True).start()
    try:
        success, value = outcomes.get(timeout=timeout)
    except queue.Empty:
        raise RuntimeError("PR evidence unavailable: total evidence deadline elapsed") from None
    if not success:
        raise value
    return value


def _evaluate(repo: Path, base: str, head: str, paths: list[str], inventory: dict | None,
              github_token: str | None, deadline: float, certification=None) -> dict:
    payload = event()
    if (payload.get("before") != base or payload.get("after") != head
            or git(repo, "rev-parse", "HEAD") != head or git(repo, "rev-parse", "HEAD^") != base
            or git(repo, "status", "--porcelain", "--untracked-files=normal")):
        raise ValueError("main push identity changed")
    if __package__:
        from .pr_reuse_github import fetch_merged_pr_receipts
    else:
        from pr_reuse_github import fetch_merged_pr_receipts
    receipts = fetch_merged_pr_receipts(os.environ["GITHUB_REPOSITORY"], head, base, github_token, deadline=deadline)
    if receipts["repository"]["id"] != payload["repository"]["id"]:
        raise ValueError("repository identity changed")
    tree = git(repo, "rev-parse", "HEAD^{tree}")
    if receipts["run"].get("execution_tree") != tree:
        raise ValueError("PR execution tree differs from main")
    if time.monotonic() >= deadline:
        raise RuntimeError("PR evidence unavailable: total evidence deadline elapsed")
    inputs = pr_reuse_inputs.capture(repo)
    if time.monotonic() >= deadline:
        raise RuntimeError("PR evidence unavailable: total evidence deadline elapsed")
    if inventory is None:
        packages, binding, profile, count = certification
        if (binding.get("base") != base or binding.get("head") != head
                or binding.get("checkout_head") != head):
            raise ValueError("current planning identity differs")
        inventory = certified_inventory(repo, receipts, packages, binding, profile, count)
    result = validate_receipts(receipts, base=base, main=head, tree=tree, inventory=inventory,
                               inputs=inputs, paths=paths)
    if git(repo, "status", "--porcelain", "--untracked-files=normal"):
        raise ValueError("main source changed during evidence validation")
    return result


def try_reuse(repo: Path, base: str, head: str, paths: list[str], inventory: dict | None, log_dir: Path,
              github_token: str | None = None, *, expected_packages=None, planning_binding=None,
              profile=None, shard_count=None) -> bool:
    if os.getenv("GITHUB_ACTIONS") != "true" or os.getenv("GITHUB_EVENT_NAME") != "push":
        return False
    reason = "main_identity"
    try:
        if os.getenv("GITHUB_REF") != "refs/heads/main" or os.getenv("GITHUB_SHA") != head:
            raise ValueError("not the requested main push")
        reason = "ci_authority_changed"
        if any(path.startswith((".github/", "scripts/quality/")) for path in paths):
            raise ValueError("CI authority changed")
        reason = "evidence_or_inputs"
        deadline = time.monotonic() + REUSE_BUDGET_SECONDS
        certification = (expected_packages, planning_binding, profile, shard_count) if inventory is None else None
        result = _bounded(lambda: _evaluate(repo, base, head, paths, inventory, github_token, deadline, certification),
                          REUSE_BUDGET_SECONDS)
        reason = "record_write"
        temporary = log_dir / "verified-reuse.json.tmp"
        temporary.write_text(json.dumps(result, sort_keys=True) + "\n", encoding="utf-8")
        temporary.replace(log_dir / "verified-reuse.json")
        print("Main Go verification: reused original PR execution; see verified-reuse.json.", flush=True)
        return True
    except Exception as error:
        detail = str(error) if isinstance(error, RuntimeError) and str(error).startswith("PR evidence unavailable: ") else reason
        try:
            (log_dir / "reuse-decision.json").write_text(json.dumps({"status": "fallback", "reason": detail}) + "\n", encoding="utf-8")
        except OSError:
            pass
        print("Main Go evidence reuse unavailable (" + detail + "); executing the original affected scope.", flush=True)
        return False
