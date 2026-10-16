"""Complete partitions and real failing Go processes remain observable."""
from pathlib import Path
from dataclasses import replace
import json
import os
import random
import tempfile
import unittest

from scripts.quality import pr_test_partition as shard
from scripts.quality.runtime_test_inventory import discover, execution_plan, test_batches
from scripts.quality.runtime_test_shards import execute


class AffectedPartitionTests(unittest.TestCase):
    def test_matrix_is_bounded_and_zero_scope_retains_one_checker_job(self):
        self.assertEqual(shard.job_matrix([]), {"include": [{"index": 0, "count": 1}]})
        self.assertEqual(len(shard.job_matrix([str(i) for i in range(200)])["include"]), 4)

    def test_inventory_is_complete_disjoint_and_order_independent(self):
        inventory = {"product/heavy": [f"TestCase{i:04d}" for i in range(1001)],
                     "product/small": ["TestSmall"], "product/empty": []}
        observed = []
        empty = 0
        for index in range(4):
            selected = shard.select(inventory, index, 4)
            reversed_inventory = {name: list(reversed(tests)) for name, tests in inventory.items()}
            self.assertEqual(selected, shard.select(reversed_inventory, index, 4))
            observed.extend((name, test) for name, tests in selected.items() for test in tests)
            empty += "product/empty" in selected
        expected = [(name, test) for name, tests in inventory.items() for test in tests]
        self.assertCountEqual(observed, expected)
        self.assertEqual(len(observed), len(set(observed)))
        self.assertEqual(empty, 1)

    def test_bad_partition_and_duplicate_tests_fail(self):
        for index, count in [(-1, 4), (4, 4), (0, 0), (0, 5)]:
            with self.assertRaises(ValueError):
                shard.select({}, index, count)
        with self.assertRaises(ValueError):
            shard.select({"product": ["TestOne", "TestOne"]}, 0, 4)

    def test_real_go_shards_retain_subtests_seeds_examples_and_failure(self):
        with tempfile.TemporaryDirectory(prefix="x-science-pr-partition-") as temporary:
            root = Path(temporary)
            repo = root / "source"
            repo.mkdir()
            (repo / "go.mod").write_text("module fixture\n\ngo 1.22\n")
            (repo / "fixture_test.go").write_text('''package fixture
import ("testing"; "fmt")
func TestNested(t *testing.T) { t.Run("child", func(t *testing.T) {}) }
func TestSkipped(t *testing.T) { t.Skip("explicit condition") }
func TestAFailure(t *testing.T) { t.Fatal("real failed assertion") }
func FuzzSeed(f *testing.F) { f.Add("seed"); f.Fuzz(func(t *testing.T, s string) {}) }
func Example() { fmt.Println("example"); /* Output: example */ }
''' + "".join(f"func TestLater{i:03d}(t *testing.T) {{}}\n" for i in range(70)))
            (repo / "empty").mkdir()
            (repo / "empty/empty.go").write_text("package empty\n")
            inventory = discover(repo, ["fixture", "fixture/empty"], False)
            results, observed, empty_processes = [], [], 0
            for index in range(4):
                selected = shard.select(inventory, index, 4)
                plan = execution_plan("pr", index, False, inventory, selected, batch_size=64)
                evidence = root / f"evidence-{index}"
                results.append(execute(repo, plan, evidence))
                events = [json.loads(line) for line in (evidence / "events.jsonl").read_text().splitlines()]
                observed.extend(events)
                empty_processes += sum(command[-1] == "fixture/empty" for command in plan["commands"])
            self.assertEqual(results.count(1), 1)
            self.assertEqual(results.count(0), 3)
            self.assertEqual(empty_processes, 1)
            terminal = {(event.get("Package"), event.get("Test")): event["Action"] for event in observed
                        if event["Action"] in {"pass", "skip", "fail"} and event.get("Test")}
            self.assertEqual(terminal["fixture", "TestNested/child"], "pass")
            self.assertEqual(terminal["fixture", "FuzzSeed/seed#0"], "pass")
            self.assertEqual(terminal["fixture", "Example"], "pass")
            self.assertEqual(terminal["fixture", "TestSkipped"], "skip")
            self.assertEqual(terminal["fixture", "TestAFailure"], "fail")
            # A failure retains the complete plan but must not start later batches.
            plan = execution_plan("pr", 0, False, inventory, inventory, batch_size=64)
            evidence = root / "failure-stops"
            self.assertEqual(execute(repo, plan, evidence), 1)
            summary = json.loads((evidence / "summary.json").read_text())
            self.assertEqual(summary["batch_exit_codes"], [1])
            self.assertEqual(summary["executed_top_level"], 64)
            self.assertEqual(summary["missing_tests"], [["fixture", name] for name in test_batches(inventory["fixture"], False, 64)[1]])
            self.assertEqual(summary["missing_packages"], ["fixture/empty"])
            self.assertEqual(summary["unstarted_process_batches"], 2)
            for command in plan["commands"]:
                self.assertIn("-count=1", command)
                self.assertIn("-timeout=30m", command)
            self.assertTrue(all(len(batch) <= 64 for batch in test_batches(inventory["fixture"], False, 64)))
            # These use genuine Go events, with a deliberately inconsistent plan.
            for kind in ["missing", "unexpected", "duplicate"]:
                selected = {"fixture": ["TestNested"]}
                plan = execution_plan("pr", 0, False, inventory, selected, batch_size=64)
                if kind != "duplicate":
                    plan["commands"][0][plan["commands"][0].index("-run") + 1] = "^$" if kind == "missing" else "^TestSkipped$"
                else:
                    plan["commands"] *= 2
                evidence = root / kind
                self.assertEqual(execute(repo, plan, evidence), 1)
                summary = json.loads((evidence / "summary.json").read_text())
                self.assertTrue(summary[{"missing": "missing_tests", "unexpected": "unexpected_tests",
                                         "duplicate": "duplicate_tests"}[kind]])


class CostProfileTests(unittest.TestCase):
    environment = {"go_version": "go1.26.0", "goos": "linux", "goarch": "amd64", "race": False, "batch_size": 64}

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="x-science-pr-costs-")
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "costs.json"
        self.value = {"schema": shard.PROFILE_SCHEMA, "environment": dict(self.environment),
                      "observed_at_epoch": 100, "valid_until_epoch": 200,
                      "default_test_ms": 1000, "default_batch_ms": 2200,
                      "costs": {"product/heavy": {"TestSlow": 78000}}}
        self.save()

    def save(self):
        self.path.write_text(json.dumps(self.value))

    def load(self, anchor=150, environment=None):
        return shard.load_profile(self.path, environment or self.environment, anchor)

    def test_strict_active_profile_and_immutable_expiry_boundaries(self):
        for anchor, status, reason in [(99, "fallback", "anchor precedes observations"),
                                      (100, "active", "compatible profile"),
                                      (199, "active", "compatible profile"),
                                      (200, "fallback", "expired profile")]:
            with self.subTest(anchor=anchor):
                profile = self.load(anchor)
                self.assertEqual((profile.status, profile.reason), (status, reason))
                self.assertEqual(profile.anchor_epoch, anchor)
                self.assertEqual(bool(profile.costs), status == "active")
                self.assertEqual(len(profile.sha256), 64)
                self.assertEqual(profile, self.load(anchor))

    def test_missing_incompatible_and_expired_profiles_keep_positive_complete_defaults(self):
        inventory = {"product/heavy": ["TestSlow", "TestUnknown", "TestSkippedHistory", "TestFailedHistory"]}
        profiles = [self.load(200)]
        for field, value in [("go_version", "go1.27.0"), ("goos", "windows"),
                             ("goarch", "arm64"), ("race", True), ("batch_size", 32)]:
            profiles.append(self.load(environment={**self.environment, field: value}))
        self.path.unlink()
        profiles.append(self.load())
        expected = shard.assignment(inventory, 4)
        for profile in profiles:
            planned = shard.assignment(inventory, 4, profile)
            self.assertEqual(planned["shards"], expected["shards"])
            self.assertEqual(planned["metadata"]["default_test_count"], 4)
            self.assertEqual(profile.default_test_ms, 1000)
            self.assertEqual(profile.default_batch_ms, 2200)
            self.assertEqual(planned["metadata"]["profiled_test_count"], 0)

    def test_invalid_costs_fail_even_when_profile_is_expired(self):
        for value in [0, -1, True, False, 1.5, "1000", None, 1800001, 10**100, float("nan"), float("inf")]:
            with self.subTest(value=value):
                self.value["costs"]["product/heavy"]["TestSlow"] = value
                self.save()
                with self.assertRaises(ValueError):
                    self.load(200)
        for field in ["default_test_ms", "default_batch_ms"]:
            self.value["costs"]["product/heavy"]["TestSlow"] = 78000
            self.value[field] = False
            self.save()
            with self.assertRaises(ValueError):
                self.load()
            self.value[field] = 1000

    def test_duplicate_json_keys_and_test_identities_fail(self):
        text = self.path.read_text()
        for bad in [text.replace('"schema":', '"schema": "duplicate", "schema":', 1),
                    text.replace('"TestSlow": 78000', '"TestSlow": 1000, "TestSlow": 78000'),
                    text.replace('"goos":', '"goos": "duplicate", "goos":', 1),
                    text.replace('"product/heavy":', '"product/heavy": {}, "product/heavy":', 1)]:
            self.path.write_text(bad)
            with self.assertRaisesRegex(ValueError, "duplicate"):
                self.load()

    def test_schema_environment_names_ranges_and_bytes_are_bounded(self):
        initial = json.loads(self.path.read_text())
        mutations = [{**initial, "schema": "unknown"}, {**initial, "extra": 1},
                     {**initial, "observed_at_epoch": True}, {**initial, "valid_until_epoch": 100},
                     {**initial, "valid_until_epoch": 100 + 30*86400 + 1},
                     {**initial, "costs": []}, {**initial, "costs": {"../outside": {"TestSlow": 1}}},
                     {**initial, "costs": {"product/heavy": {"TestParent/child": 1}}},
                     {**initial, "environment": {**self.environment, "race": 0}},
                     {**initial, "environment": {**self.environment, "batch_size": 65}},
                     {**initial, "environment": {**self.environment, "extra": 1}}]
        for value in mutations:
            self.path.write_text(json.dumps(value))
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.load()
        for data in [b"{invalid", b"\xff", b" " * (shard.MAX_PROFILE_BYTES + 1),
                     b"[" * 1100 + b"0" + b"]" * 1100]:
            self.path.write_bytes(data)
            with self.assertRaises(ValueError):
                self.load()

    def test_symlinks_traversal_and_nonregular_profiles_fail(self):
        link = self.path.with_name("link.json")
        link.symlink_to(self.path)
        with self.assertRaises(ValueError):
            shard.load_profile(link, self.environment, 150)
        directory_link = self.path.parent / "alias"
        directory_link.symlink_to(self.path.parent, target_is_directory=True)
        with self.assertRaises(ValueError):
            shard.load_profile(directory_link / "costs.json", self.environment, 150)
        with self.assertRaises(ValueError):
            shard.load_profile(self.path.parent / "nested" / ".." / "costs.json", self.environment, 150)
        with self.assertRaises((ValueError, OSError)):
            shard.load_profile(self.path.parent, self.environment, 150)
        if hasattr(os, "mkfifo"):
            fifo = self.path.with_name("fifo")
            os.mkfifo(fifo)
            with self.assertRaises(ValueError):
                shard.load_profile(fifo, self.environment, 150)

    def test_checked_in_sparse_profile_is_valid_and_consumed(self):
        path = Path(__file__).with_name("pr_test_costs.json")
        value = json.loads(path.read_text())
        profile = shard.load_profile(path, self.environment, value["observed_at_epoch"])
        self.assertEqual(profile.status, "active")
        self.assertEqual(len(profile.costs), 62)
        module = (path.parents[2] / "go.mod").read_text().splitlines()[0].removeprefix("module ")
        self.assertTrue(all(package.startswith(module + "/") for package in value["costs"]))
        inventory = {package: sorted(tests) for package, tests in value["costs"].items()}
        planned = shard.assignment(inventory, 4, profile)
        self.assertEqual(planned["metadata"]["profiled_test_count"], 62)
        self.assertLess(path.stat().st_size, 6000)


class BalancedAssignmentTests(unittest.TestCase):
    def test_global_lpt_uses_integer_cost_and_lexical_ties(self):
        profile = shard.CostProfile(costs={("a", "TestSlow"): 78000, ("b", "TestSlow"): 39000})
        result = shard.assignment({"a": ["TestFast", "TestSlow"], "b": ["TestSlow", "TestFast"]}, 2, profile)
        self.assertIn("TestSlow", result["shards"][0]["a"])
        self.assertIn("TestSlow", result["shards"][1]["b"])
        self.assertLess(max(result["metadata"]["estimated_load_ms"]), 82000)
        self.assertTrue(all(type(load) is int for load in result["metadata"]["estimated_load_ms"]))
        tied = shard.assignment({"b": ["TestB"], "a": ["TestA"]}, 2)
        self.assertEqual(tied["shards"], [{"a": ["TestA"]}, {"b": ["TestB"]}])

    def test_all_counts_input_orders_and_cost_orders_preserve_exact_assignment(self):
        inventory = {"product/heavy": [f"TestCase{i:04d}" for i in range(270)],
                     "product/small": ["TestCase0000", "Example", "FuzzSeed"], "product/empty": []}
        costs = {("product/heavy", "TestCase0000"): 1800000, ("product/small", "TestCase0000"): 5000}
        randomizer = random.Random(7)
        for count in range(1, 5):
            expected = shard.assignment(inventory, count, shard.CostProfile(costs=costs))
            for _ in range(10):
                items = list(inventory.items())
                randomizer.shuffle(items)
                shuffled = {}
                for package, names in items:
                    names = list(names)
                    randomizer.shuffle(names)
                    shuffled[package] = names
                result = shard.assignment(shuffled, count, shard.CostProfile(costs=dict(reversed(list(costs.items())))))
                self.assertEqual(result, expected)
                shard.validate_assignment(inventory, result["shards"], count)

    def test_new_deleted_renamed_tests_packages_and_unused_weights_do_not_change_eligibility(self):
        old = {"a": ["TestOld", "TestSame"], "b": ["TestSame"], "empty": []}
        changed = {"a": ["TestRenamed", "TestSame", "TestNew"], "b": ["TestSame"], "new": ["FuzzNew"], "empty": []}
        profile = shard.CostProfile(costs={("a", "TestOld"): 78000, ("a", "TestSame"): 6000})
        for inventory in [old, changed, {}, {"empty": []}]:
            result = shard.assignment(inventory, 4, profile)
            shard.validate_assignment(inventory, result["shards"], 4)
        result = shard.assignment(changed, 4, profile)
        self.assertEqual(result["metadata"]["unused_profile_test_count"], 1)
        self.assertEqual(result["metadata"]["profiled_test_count"], 1)
        self.assertEqual(result["metadata"]["default_test_count"], 4)
        flattened = {(p, t) for part in result["shards"] for p, names in part.items() for t in names}
        self.assertNotIn(("a", "TestOld"), flattened)
        self.assertIn(("a", "TestSame"), flattened)
        self.assertIn(("b", "TestSame"), flattened)

    def test_batch_fee_counts_each_new_64_test_process(self):
        inventory = {"a": [f"TestCase{i:04d}" for i in range(130)], "empty": []}
        for count in range(1, 5):
            result = shard.assignment(inventory, count)
            batches = sum((len(names) + 63)//64 for part in result["shards"] for names in part.values() if names)
            self.assertEqual(sum(result["metadata"]["estimated_load_ms"]), 130000 + batches * 2200)
            self.assertEqual(sum("empty" in part for part in result["shards"]), 1)

    def test_illegal_assignment_and_inventory_fail_closed(self):
        inventory = {"a": ["TestOne"], "empty": []}
        bad = [[{"a": ["TestOne"]}, {}], [{"a": ["TestOne"], "empty": []}, {"a": ["TestOne"]}],
               [{"a": ["TestOther"], "empty": []}, {}], [{"a": ["TestOne"], "empty": []}, {"empty": []}],
               [{"a": ["TestOne"], "empty": [], "unexpected": []}, {}], [{"a": [], "empty": []}, {}]]
        for parts in bad:
            with self.assertRaises(ValueError):
                shard.validate_assignment(inventory, parts, 2)
        for bad_inventory in [{"a": ["TestOne", "TestOne"]}, {"a": ["TestOne/sub"]},
                              {"../a": []}, {"a": "TestOne"}, {"a": [False]}, []]:
            with self.assertRaises(ValueError):
                shard.assignment(bad_inventory, 2)
        for index, count in [(True, 4), (0, True), (0, 1.5)]:
            with self.assertRaises(ValueError):
                shard.select({}, index, count)


if __name__ == "__main__":
    unittest.main()
