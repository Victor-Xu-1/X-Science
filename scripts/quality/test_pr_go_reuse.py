"""Focused identity, completeness and fail-closed reuse regressions."""
import copy
import json
import tempfile
import threading
import unittest
from pathlib import Path
from unittest import mock

from scripts.quality import pr_go_reuse as reuse, pr_test_partition as partition, runtime_test_inventory


def encoded(value):
    return json.dumps(value, sort_keys=True).encode()


def fixture():
    inventory = {"example/pkg": ["TestOne", "TestTwo"]}
    assignments = [{"example/pkg": ["TestOne"]}, {"example/pkg": ["TestTwo"]}]
    assignment_digest = partition.digest(assignments)
    inventory_digest = partition.digest(inventory)
    inputs = {"go": {"GOVERSION": "go1.26.0"}, "python_dependencies": {"example": "1"}, "node": "v22.21.0"}
    artifacts = []
    for index, selected in enumerate(assignments):
        name = selected["example/pkg"][0]
        commands = runtime_test_inventory.execution_plan("pr", index, False, inventory, selected, batch_size=64)["commands"]
        plan = {"selected": selected, "commands": commands, "inventory_sha256": inventory_digest,
                "partition": {"base": "base", "head": "synthetic", "checkout_head": "synthetic",
                              "source_state": "clean", "shard_index": index, "shard_count": 2,
                              "inventory_sha256": inventory_digest, "assignment_sha256": assignment_digest}}
        summary = {"exit_code": 0, "unstarted_process_batches": 0, "executed_top_level": 1,
                   "missing_tests": [], "unexpected_tests": [], "missing_packages": [],
                   "duplicate_tests": [], "batch_exit_codes": [0]}
        scope = {"base": "base", "head": "synthetic", "shard_index": index, "shard_count": 2,
                 "changed_paths": ["internal/example.go"], "packages": ["example/pkg"]}
        files = {"scope.json": encoded(scope), "plan.json": encoded(plan), "summary.json": encoded(summary),
                 "events.jsonl": encoded({"Package": "example/pkg", "Test": name, "Action": "pass"}) + b"\n"}
        provenance = {"schema": reuse.SCHEMA, "repository_id": 17, "pr_number": 5, "base": "base",
                      "pr_head": "pr-head", "execution_sha": "synthetic", "tree": "tree", "run_id": 40,
                      "run_attempt": 1, "index": index, "count": 2, "job": "pr-go-shards", "inputs": inputs,
                      "files": {key: reuse.digest(files[key]) for key in reuse.RECEIPT_FILES}}
        files["provenance.json"] = encoded(provenance)
        artifacts.append({"index": index, "id": 100 + index, "digest": "sha256:" + "a" * 64, "files": files})
    receipts = {"repository": {"id": 17}, "pr": {"number": 5, "head_sha": "pr-head"},
                "run": {"id": 40, "attempt": 1, "workflow_id": 44, "execution_sha": "synthetic"},
                "checks": [{"id": 99, "app_id": 15368}], "jobs": [{"id": 98, "index": 0}], "artifacts": artifacts}
    return receipts, inventory, inputs


def certified_fixture(repo):
    receipts, inventory, inputs = fixture()
    inventory["example/empty"] = []
    environment = {"go_version": "go1.26.0", "goos": "linux", "goarch": "amd64", "race": False, "batch_size": 64}
    path = repo / partition.PROFILE_PATH
    path.parent.mkdir(parents=True)
    path.write_bytes(encoded({"schema": partition.PROFILE_SCHEMA, "environment": environment,
                             "observed_at_epoch": 10, "valid_until_epoch": 1000,
                             "default_test_ms": 1000, "default_batch_ms": 2200, "costs": {}}))
    profile = partition.load_profile(path, environment, 100)
    original = partition.load_profile(path, environment, 90)
    expected = partition.assignment(inventory, 2, original)
    inputs["source_sha256"] = {name: reuse.digest(name.encode()) for name in reuse.pr_reuse_inputs.SOURCE_INPUTS}
    inputs["source_sha256"][partition.PROFILE_PATH] = profile.sha256
    for index, artifact in enumerate(receipts["artifacts"]):
        files = artifact["files"]
        selected = expected["shards"][index]
        plan = runtime_test_inventory.execution_plan("pr", index, False, inventory, selected, batch_size=64)
        plan["inventory_sha256"] = partition.digest(inventory)
        plan["partition"] = {**expected["metadata"], "base": "base", "head": "synthetic",
                             "checkout_head": "synthetic", "source_state": "clean", "shard_index": index,
                             "anchor_epoch": 90, "environment": environment}
        files["plan.json"] = encoded(plan)
        scope = json.loads(files["scope.json"])
        scope["packages"] = sorted(inventory)
        files["scope.json"] = encoded(scope)
        files["summary.json"] = encoded({"exit_code": 0, "unstarted_process_batches": 0,
            "executed_top_level": sum(map(len, selected.values())), "missing_tests": [], "unexpected_tests": [],
            "missing_packages": [], "duplicate_tests": [], "batch_exit_codes": [0] * len(plan["commands"])})
        files["events.jsonl"] = b"".join(encoded({"Package": package, "Test": name, "Action": "pass"}) + b"\n"
                                             for package, names in selected.items() for name in names)
        provenance = json.loads(files["provenance.json"])
        provenance["inputs"] = inputs
        provenance["files"] = {name: reuse.digest(files[name]) for name in reuse.RECEIPT_FILES}
        files["provenance.json"] = encoded(provenance)
    receipts["run"]["execution_tree"] = "tree"
    binding = {"base": "base", "head": "main", "checkout_head": "main", "anchor_epoch": 100,
               "source_state": "clean", "environment": environment}
    return receipts, inventory, inputs, binding, profile


class GoReuseTest(unittest.TestCase):
    def attempt_certified(self, path, receipts, inventory, inputs, binding, profile):
        env = {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main",
               "GITHUB_SHA": "main", "GITHUB_REPOSITORY": "owner/repo"}
        with mock.patch.dict("os.environ", env, clear=True), \
             mock.patch.object(reuse, "event", return_value={"before": "base", "after": "main", "repository": {"id": 17}}), \
             mock.patch.object(reuse, "git", side_effect=["main", "base", "", "tree", ""]), \
             mock.patch.object(reuse.pr_reuse_inputs, "capture", return_value=inputs), \
             mock.patch("scripts.quality.pr_reuse_github.fetch_merged_pr_receipts", return_value=receipts) as fetch:
            accepted = reuse.try_reuse(path, "base", "main", ["internal/example.go"], path,
                expected_packages=sorted(inventory), planning_binding=binding, profile=profile, shard_count=2)
            fetch.assert_called_once()
            return accepted

    def test_certified_full_inventory_keeps_empty_packages_and_original_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            receipts, inventory, inputs, binding, profile = certified_fixture(path)
            self.assertEqual(reuse.certified_inventory(path, receipts, sorted(inventory), binding, profile, 2), inventory)
            self.assertTrue(self.attempt_certified(path, receipts, inventory, inputs, binding, profile))
            result = json.loads((path / "verified-reuse.json").read_bytes())
            self.assertEqual(result["execution_sha"], "synthetic")
            self.assertEqual(result["main_sha"], "main")
            self.assertFalse((path / "events.jsonl").exists())

    def test_certified_partial_conflicting_or_legacy_evidence_falls_back(self):
        for failure in ("missing_test", "overlap", "empty_package", "legacy_source", "profile", "expired", "digest", "count"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                path = Path(directory)
                receipts, inventory, inputs, binding, profile = certified_fixture(path)
                if failure == "legacy_source":
                    for artifact in receipts["artifacts"]:
                        prov = json.loads(artifact["files"]["provenance.json"])
                        prov["inputs"]["source_sha256"]["scripts/quality/pr_fast_scope.py"] = "old-producer"
                        artifact["files"]["provenance.json"] = encoded(prov)
                elif failure == "expired":
                    profile = partition.load_profile(path / partition.PROFILE_PATH, profile.environment, 1000)
                    binding["anchor_epoch"] = 1000
                elif failure == "count":
                    receipts["artifacts"].pop()
                else:
                    files = receipts["artifacts"][0]["files"]
                    plan = json.loads(files["plan.json"])
                    if failure == "missing_test":
                        plan["selected"]["example/pkg"] = []
                    elif failure == "overlap":
                        other = json.loads(receipts["artifacts"][1]["files"]["plan.json"])
                        plan["selected"]["example/pkg"].extend(other["selected"]["example/pkg"])
                    elif failure == "empty_package":
                        for artifact in receipts["artifacts"]:
                            candidate = json.loads(artifact["files"]["plan.json"])
                            candidate["selected"].pop("example/empty", None)
                            artifact["files"]["plan.json"] = encoded(candidate)
                        plan["selected"].pop("example/empty", None)
                    elif failure == "profile":
                        plan["partition"]["profile"]["sha256"] = "other"
                    elif failure == "digest":
                        files["events.jsonl"] += b"changed\n"
                    files["plan.json"] = encoded(plan)
                if failure not in ("digest", "legacy_source"):
                    for artifact in receipts["artifacts"]:
                        files = artifact["files"]
                        prov = json.loads(files["provenance.json"])
                        prov["files"] = {name: reuse.digest(files[name]) for name in reuse.RECEIPT_FILES}
                        files["provenance.json"] = encoded(prov)
                self.assertFalse(self.attempt_certified(path, receipts, inventory, inputs, binding, profile))
                self.assertFalse((path / "verified-reuse.json").exists())

    def test_pr_input_capture_precedes_discovery_and_main_fallback_only_fetches_once(self):
        import os
        import sys
        from scripts.quality import pr_fast_scope
        steps, initial = [], {"fixture": "same"}
        def capture(*args):
            steps.append("capture")
            return initial
        def discovery(*args, **kwargs):
            steps.append("discovery")
            return {"example/pkg": ["TestOne"]}
        with tempfile.TemporaryDirectory() as directory:
            args = ["pr_fast_scope.py", "--base", "base", "--head", "main", "--log-dir", directory]
            with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(sys, "argv", args), \
                 mock.patch.object(pr_fast_scope, "changed_paths", return_value=[]), \
                 mock.patch.object(pr_fast_scope, "behavior_scope", return_value=([], [], [])), \
                 mock.patch.object(pr_fast_scope.verification_scope, "matched", return_value=[]), \
                 mock.patch.object(pr_fast_scope, "resolve_packages", return_value=(["example/pkg"], "fixture")), \
                 mock.patch.object(pr_fast_scope, "planning_inputs", return_value=({}, partition.CostProfile())), \
                 mock.patch.object(pr_fast_scope, "discover", side_effect=discovery) as discover, \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "try_reuse", return_value=False) as attempt, \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "prepare_pr_inputs", side_effect=capture), \
                 mock.patch.object(pr_fast_scope, "execute", return_value=0) as execute, \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "write_pr_provenance") as producer:
                self.assertEqual(pr_fast_scope.main(), 0)
                self.assertEqual(steps, ["capture", "discovery"])
                attempt.assert_called_once()
                discover.assert_called_once()
                execute.assert_called_once()
                self.assertIs(producer.call_args.args[-1], initial)
    def test_main_certified_reuse_precedes_discovery(self):
        import os
        import sys
        from scripts.quality import pr_fast_scope
        with tempfile.TemporaryDirectory() as directory:
            args = ["pr_fast_scope.py", "--base", "base", "--head", "main", "--log-dir", directory]
            with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(sys, "argv", args), \
                 mock.patch.object(pr_fast_scope, "changed_paths", return_value=[]), \
                 mock.patch.object(pr_fast_scope, "behavior_scope", return_value=([], [], [])), \
                 mock.patch.object(pr_fast_scope.verification_scope, "matched", return_value=[]), \
                 mock.patch.object(pr_fast_scope, "resolve_packages", return_value=(["example/pkg"], "fixture")), \
                 mock.patch.object(pr_fast_scope, "planning_inputs", return_value=({}, partition.CostProfile())), \
                 mock.patch.object(pr_fast_scope, "discover", return_value={"example/pkg": ["TestOne"]}) as discover, \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "try_reuse", return_value=True), \
                 mock.patch.object(pr_fast_scope, "execute") as execute:
                self.assertEqual(pr_fast_scope.main(), 0)
                discover.assert_not_called()
                execute.assert_not_called()

    def validate(self, receipts, inventory, inputs):
        return reuse.validate_receipts(receipts, base="base", main="main", tree="tree", inventory=inventory,
                                       inputs=inputs, paths=["internal/example.go"])

    def test_complete_receipt_keeps_original_execution_identity(self):
        receipts, inventory, inputs = fixture()
        result = self.validate(receipts, inventory, inputs)
        self.assertEqual(result["status"], "verified-reuse")
        self.assertEqual(result["execution_sha"], "synthetic")
        self.assertEqual(result["main_sha"], "main")
        self.assertEqual(result["run_attempt"], 1)
        self.assertEqual(len(result["artifacts"]), 2)

    def test_stale_or_incomplete_evidence_is_not_reused(self):
        receipts, inventory, inputs = fixture()
        cases = []
        wrong_tree = copy.deepcopy(receipts)
        prov = json.loads(wrong_tree["artifacts"][0]["files"]["provenance.json"])
        prov["tree"] = "other"
        wrong_tree["artifacts"][0]["files"]["provenance.json"] = encoded(prov)
        cases.append((wrong_tree, inventory, inputs))
        old = copy.deepcopy(receipts)
        del old["artifacts"][0]["files"]["provenance.json"]
        cases.append((old, inventory, inputs))
        missing = copy.deepcopy(receipts)
        missing["artifacts"].pop()
        cases.append((missing, inventory, inputs))
        changed = copy.deepcopy(inputs)
        changed["python_dependencies"]["example"] = "2"
        cases.append((receipts, inventory, changed))
        node_changed = copy.deepcopy(inputs)
        node_changed["node"] = None
        cases.append((receipts, inventory, node_changed))
        altered = copy.deepcopy(receipts)
        altered["artifacts"][0]["files"]["events.jsonl"] += b"bad\n"
        cases.append((altered, inventory, inputs))
        wrong_attempt = copy.deepcopy(receipts)
        wrong_attempt["run"]["attempt"] = 2
        cases.append((wrong_attempt, inventory, inputs))
        for candidate, inv, environment in cases:
            with self.subTest(candidate=cases.index((candidate, inv, environment))):
                with self.assertRaises((ValueError, KeyError)):
                    self.validate(candidate, inv, environment)

    def test_extra_missing_and_duplicate_test_events_fail_closed(self):
        receipts, inventory, inputs = fixture()
        for event in (b"", encoded({"Package": "example/pkg", "Test": "TestOther", "Action": "pass"}) + b"\n",
                      receipts["artifacts"][0]["files"]["events.jsonl"] * 2):
            candidate = copy.deepcopy(receipts)
            files = candidate["artifacts"][0]["files"]
            files["events.jsonl"] = event
            provenance = json.loads(files["provenance.json"])
            provenance["files"]["events.jsonl"] = reuse.digest(event)
            files["provenance.json"] = encoded(provenance)
            with self.subTest(event=event):
                with self.assertRaises(ValueError):
                    self.validate(candidate, inventory, inputs)

    def test_altered_commands_and_scope_reject_even_with_matching_file_digest(self):
        receipts, inventory, inputs = fixture()
        for name in ("plan.json", "scope.json"):
            candidate = copy.deepcopy(receipts)
            files = candidate["artifacts"][0]["files"]
            value = json.loads(files[name])
            if name == "plan.json":
                value["commands"] = [["go", "test", "-run", "^$"]]
            else:
                value["changed_paths"] = ["internal/unrelated.go"]
            files[name] = encoded(value)
            provenance = json.loads(files["provenance.json"])
            provenance["files"][name] = reuse.digest(files[name])
            files["provenance.json"] = encoded(provenance)
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    self.validate(candidate, inventory, inputs)

    def test_ci_change_and_bad_evidence_fall_back_without_fabricated_output(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            env = {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main",
                   "GITHUB_SHA": "main", "GITHUB_REPOSITORY": "owner/repo", "GITHUB_TOKEN": "unused"}
            with mock.patch.dict("os.environ", env, clear=True):
                self.assertFalse(reuse.try_reuse(output, "base", "main", ["scripts/quality/pr_fast_scope.py"], output))
                with mock.patch.object(reuse, "event", return_value={"before": "base", "after": "main", "repository": {"id": 17}}), \
                     mock.patch.object(reuse, "git", side_effect=["main", "base", ""]), \
                     mock.patch("scripts.quality.pr_reuse_github.fetch_merged_pr_receipts", side_effect=RuntimeError("unavailable")):
                    self.assertFalse(reuse.try_reuse(output, "base", "main", ["internal/example.go"], output))
                self.assertFalse((output / "verified-reuse.json").exists())

    def test_producer_binds_exact_pr_execution_and_file_digests(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            for name in reuse.RECEIPT_FILES:
                (output / name).write_bytes(name.encode())
            event = {"repository": {"id": 17}, "pull_request": {"number": 5,
                     "base": {"sha": "base", "repo": {"id": 17}},
                     "head": {"sha": "pr-head", "repo": {"id": 17}}}}
            env = {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request", "GITHUB_SHA": "synthetic",
                   "GITHUB_RUN_ID": "40", "GITHUB_RUN_ATTEMPT": "1", "GITHUB_JOB": "pr-go-shards"}
            with mock.patch.dict("os.environ", env, clear=True), mock.patch.object(reuse, "event", return_value=event), \
                 mock.patch.object(reuse, "git", side_effect=["synthetic", "", "tree"]), \
                 mock.patch.object(reuse.pr_reuse_inputs, "capture", return_value={"go": "known"}):
                reuse.write_pr_provenance(output, "base", "synthetic", output, 0, 1, {"go": "known"})
            record = json.loads((output / "provenance.json").read_text(encoding="utf-8"))
            self.assertEqual(record["execution_sha"], "synthetic")
            self.assertEqual(record["pr_head"], "pr-head")
            self.assertEqual(record["tree"], "tree")
            self.assertEqual(record["files"], {name: reuse.digest(name.encode()) for name in reuse.RECEIPT_FILES})

    def test_inputs_cover_transitive_test_dependencies_not_unrelated_packages(self):
        distributions = {
            "root": mock.Mock(version="1.0", requires=["dep>=2", "optional; extra == 'extra'", "inactive; python_version < '2'"]),
            "dep": mock.Mock(version="2.1", requires=[]),
            "optional": mock.Mock(version="3", requires=[]),
        }
        with tempfile.TemporaryDirectory() as directory:
            requirements = Path(directory) / "requirements.txt"
            requirements.write_text("root==1.0\n", encoding="utf-8")
            with mock.patch.object(reuse.pr_reuse_inputs.importlib.metadata, "distribution", side_effect=distributions.__getitem__):
                self.assertEqual(reuse.pr_reuse_inputs.python_dependencies(requirements), {"dep": "2.1", "root": "1.0"})
                requirements.write_text("root[extra]==1.0\n", encoding="utf-8")
                self.assertEqual(reuse.pr_reuse_inputs.python_dependencies(requirements),
                                 {"dep": "2.1", "optional": "3", "root": "1.0"})

    def test_existing_executor_runs_once_on_fallback_and_propagates_failure(self):
        from scripts.quality import pr_fast_scope

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            with mock.patch.object(pr_fast_scope.pr_go_reuse, "try_reuse", return_value=False), \
                 mock.patch.object(pr_fast_scope, "execute", return_value=7) as execute, \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "write_pr_provenance") as producer:
                self.assertEqual(pr_fast_scope.execute_affected(path, "base", "main", [], {}, {}, path, 0, 1, None), 7)
                execute.assert_called_once()
                producer.assert_not_called()
            with mock.patch.object(pr_fast_scope.pr_go_reuse, "try_reuse", return_value=False), \
                 mock.patch.object(pr_fast_scope, "execute", return_value=0) as execute, \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "write_pr_provenance") as producer:
                self.assertEqual(pr_fast_scope.execute_affected(path, "base", "main", [], {}, {}, path, 0, 1, None), 0)
                execute.assert_called_once()
                producer.assert_called_once()

    def test_token_is_removed_before_discovery_and_fallback_execution(self):
        import os
        import sys
        from scripts.quality import pr_fast_scope

        def no_token(*args, **kwargs):
            self.assertNotIn("GITHUB_TOKEN", os.environ)
            return {"example/pkg": ["TestOne"]}

        def execute_without_token(*args, **kwargs):
            self.assertNotIn("GITHUB_TOKEN", os.environ)
            return 0

        with tempfile.TemporaryDirectory() as directory:
            args = ["pr_fast_scope.py", "--base", "base", "--head", "main", "--log-dir", directory]
            with mock.patch.dict(os.environ, {"GITHUB_TOKEN": "secret"}), mock.patch.object(sys, "argv", args), \
                 mock.patch.object(pr_fast_scope, "changed_paths", return_value=[]), \
                 mock.patch.object(pr_fast_scope, "behavior_scope", return_value=([], [], [])), \
                 mock.patch.object(pr_fast_scope.verification_scope, "matched", return_value=[]), \
                 mock.patch.object(pr_fast_scope, "resolve_packages", return_value=(["example/pkg"], "fixture")), \
                 mock.patch.object(pr_fast_scope, "planning_inputs", return_value=({}, partition.CostProfile())), \
                 mock.patch.object(pr_fast_scope, "discover", side_effect=no_token), \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "try_reuse", return_value=False) as reuse_call, \
                 mock.patch.object(pr_fast_scope, "execute", side_effect=execute_without_token), \
                 mock.patch.object(pr_fast_scope.pr_go_reuse, "write_pr_provenance"):
                self.assertEqual(pr_fast_scope.main(), 0)
                self.assertEqual(reuse_call.call_args.args[-1], "secret")

    def test_inputs_record_actual_planner_flags_and_source_hashes(self):
        repo = Path(__file__).resolve().parents[2]
        go = {"GOVERSION": "go1.26.0", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1",
              "GOEXPERIMENT": "", "CGO_ENABLED": "0", "GOFLAGS": "", "GOTOOLCHAIN": "local",
              "CC": "gcc", "CXX": "g++", "CGO_CFLAGS": "-O2", "CGO_CPPFLAGS": "",
              "CGO_CXXFLAGS": "-O2", "CGO_LDFLAGS": "-O2"}
        completed = mock.Mock(stdout=json.dumps(go))
        with mock.patch.object(reuse.pr_reuse_inputs.subprocess, "run", return_value=completed), \
             mock.patch.object(reuse.pr_reuse_inputs, "python_dependencies", return_value={"example": "1"}), \
             mock.patch.object(reuse.pr_reuse_inputs, "command", return_value="fixture-version"):
            captured = reuse.pr_reuse_inputs.capture(repo)
        self.assertEqual(captured["go"]["GOVERSION"], go["GOVERSION"])
        self.assertEqual(captured["go"]["CGO_CFLAGS"], "sha256:" + reuse.digest(b"-O2"))
        self.assertEqual(captured["test_flags"], ["go", "test", "-buildvcs=false", "-json", "-p", "1", "-timeout=30m", "-count=1"])
        for path in ("go.mod", "go.sum", "scripts/quality/pr_fast_scope.py", "scripts/quality/pr_test_costs.json"):
            self.assertEqual(captured["source_sha256"][path], reuse.digest((repo / path).read_bytes()))

    def test_dirty_main_and_changed_producer_inputs_fall_back(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            env = {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main", "GITHUB_SHA": "main"}
            with mock.patch.dict("os.environ", env, clear=True), \
                 mock.patch.object(reuse, "event", return_value={"before": "base", "after": "main"}), \
                 mock.patch.object(reuse, "git", side_effect=["main", "base", " M changed.go"]), \
                 mock.patch("scripts.quality.pr_reuse_github.fetch_merged_pr_receipts") as fetch:
                self.assertFalse(reuse.try_reuse(output, "base", "main", ["internal/example.go"], output))
                fetch.assert_not_called()
            with mock.patch.dict("os.environ", {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request"}, clear=True), \
                 mock.patch.object(reuse.pr_reuse_inputs, "capture", return_value={"node": "v24"}):
                reuse.write_pr_provenance(output, "base", "head", output, 0, 1, {"node": "v22"})
                self.assertFalse((output / "provenance.json").exists())
            with mock.patch.dict("os.environ", {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request"}, clear=True), \
                 mock.patch.object(reuse.pr_reuse_inputs, "capture") as capture:
                reuse.write_pr_provenance(output, "base", "head", output, 0, 1, None)
                capture.assert_not_called()
                self.assertFalse((output / "provenance.json").exists())

    def test_cgo_capture_uses_actual_compiler_and_hashes_flags(self):
        repo = Path(__file__).resolve().parents[2]
        go = {"GOVERSION": "go1.26.0", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1",
              "GOEXPERIMENT": "", "CGO_ENABLED": "1", "GOFLAGS": "", "GOTOOLCHAIN": "local",
              "CC": "clang", "CXX": "clang++", "CGO_CFLAGS": "-O3", "CGO_CPPFLAGS": "",
              "CGO_CXXFLAGS": "-O2", "CGO_LDFLAGS": "-lm"}
        with mock.patch.object(reuse.pr_reuse_inputs.subprocess, "run", return_value=mock.Mock(stdout=json.dumps(go))), \
             mock.patch.object(reuse.pr_reuse_inputs, "python_dependencies", return_value={}), \
             mock.patch.object(reuse.pr_reuse_inputs, "command", side_effect=lambda *args: " ".join(args)) as command:
            captured = reuse.pr_reuse_inputs.capture(repo)
        self.assertIn(mock.call("clang", "--version"), command.call_args_list)
        self.assertIn(mock.call("clang++", "--version"), command.call_args_list)
        self.assertEqual(captured["go"]["CGO_CFLAGS"], "sha256:" + reuse.digest(b"-O3"))

    def test_deadline_falls_back_and_late_result_cannot_write_reuse_record(self):
        started, release, finished = threading.Event(), threading.Event(), threading.Event()

        def delayed(*args):
            started.set()
            release.wait(1)
            finished.set()
            return {"status": "verified-reuse"}

        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            env = {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main",
                   "GITHUB_SHA": "main"}
            with mock.patch.dict("os.environ", env, clear=True), mock.patch.object(reuse, "_evaluate", side_effect=delayed), \
                 mock.patch.object(reuse, "REUSE_BUDGET_SECONDS", 0.02):
                self.assertFalse(reuse.try_reuse(output, "base", "main", ["internal/example.go"], output, "token"))
                self.assertTrue(started.is_set())
                self.assertFalse((output / "verified-reuse.json").exists())
                decision = json.loads((output / "reuse-decision.json").read_text())
                self.assertIn("deadline", decision["reason"])
                release.set()
                self.assertTrue(finished.wait(1))
                self.assertFalse((output / "verified-reuse.json").exists())


if __name__ == "__main__":
    unittest.main()
