"""Phase evidence regressions with controlled Python children, never Go/npm."""
import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

from scripts.quality import pr_fast_scope as scope
from scripts.quality import runtime_test_shards as shards

PACKAGE = "fixture/phase"


def child_command(name="TestChild", code=0, gate=None, started=None):
    events = [{"Action": "run", "Package": PACKAGE, "Test": name},
              {"Action": "pass" if code == 0 else "fail", "Package": PACKAGE,
               "Test": name, "Elapsed": 0.01},
              {"Action": "pass" if code == 0 else "fail", "Package": PACKAGE, "Elapsed": 0.01}]
    program = "import json,time,sys; from pathlib import Path\n"
    if started is not None:
        program += f"Path({str(started)!r}).write_text('started')\n"
    if gate is not None:
        program += (f"deadline=time.monotonic()+3\nwhile not Path({str(gate)!r}).exists():\n"
                    " if time.monotonic()>deadline: sys.exit(29)\n time.sleep(0.005)\n")
    program += f"events={events!r}\nfor event in events: print(json.dumps(event),flush=True)\nsys.exit({code})\n"
    return [sys.executable, "-c", program], "".join(json.dumps(event) + "\n" for event in events)


def child_plan(commands, names):
    return {"group": "fixture", "index": 0, "race": False, "packages": [PACKAGE] if names else [],
            "selected": {PACKAGE: names} if names else {}, "inventory_count": len(names),
            "inventory_sha256": "fixture-inventory", "commands": commands, "partition": {}}


class RuntimePhaseEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="x-science-phase-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.repo = self.root / "source"
        self.repo.mkdir()
        self.log_dir = self.root / "evidence"

    def records(self):
        path = self.log_dir / "runtime-phases.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.is_file() else []

    def test_batch_start_is_durable_while_controlled_child_is_still_running(self):
        gate, started = self.root / "release", self.root / "child-started"
        command, raw = child_command(gate=gate, started=started)
        plan = child_plan([command], ["TestChild"])
        result, errors = [], []

        def invoke():
            try:
                result.append(shards.execute(self.repo, plan, self.log_dir))
            except BaseException as error:
                errors.append(error)

        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            worker = threading.Thread(target=invoke)
            worker.start()
            try:
                deadline = time.monotonic() + 3
                while not started.exists() and worker.is_alive() and time.monotonic() < deadline:
                    time.sleep(0.005)
                barrier_reached = started.is_file() and worker.is_alive()
                held = self.records()
            finally:
                gate.write_text("release")
                worker.join(timeout=5)
        self.assertTrue(barrier_reached, f"controlled child did not reach live barrier: {errors}")
        self.assertFalse(worker.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual(result, [0])
        self.assertEqual((self.log_dir / "events.jsonl").read_text(), raw)
        self.assertTrue(any(row["phase"] == "batch" and row["event"] == "start" for row in held),
                        "running child had no durable batch-start evidence")
        self.assertFalse(any(row["phase"] == "batch" and row["event"] == "finish" for row in held))
        finished = next(row for row in self.records() if row["phase"] == "batch" and row["event"] == "finish")
        self.assertEqual(finished["exit_code"], 0)
        self.assertEqual(finished["status"], "success")
        self.assertGreaterEqual(finished["elapsed_seconds"], 0)
        self.assertIn("started_at", finished)
        self.assertIn("finished_at", finished)
        self.assertEqual(json.loads((self.log_dir / "plan.json").read_text()), plan)

    def test_failed_batch_records_real_exit_and_leaves_later_batch_unstarted(self):
        first, raw = child_command("TestFailure", 17)
        marker = self.root / "must-not-start"
        second, _ = child_command("TestLater", started=marker)
        plan = child_plan([first, second], ["TestFailure", "TestLater"])
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            result = shards.execute(self.repo, plan, self.log_dir)
        self.assertEqual(result, 1)
        self.assertFalse(marker.exists())
        self.assertEqual((self.log_dir / "events.jsonl").read_text(), raw)
        finishes = [row for row in self.records() if row["phase"] == "batch" and row["event"] == "finish"]
        self.assertEqual(len(finishes), 1)
        self.assertEqual(finishes[0]["exit_code"], 17)
        self.assertEqual(finishes[0]["status"], "failed")
        summary = json.loads((self.log_dir / "summary.json").read_text())
        self.assertEqual(summary["batch_exit_codes"], [17])
        self.assertEqual(summary["planned_process_batches"], 2)
        self.assertEqual(summary["unstarted_process_batches"], 1)
        self.assertEqual(summary["missing_tests"], [[PACKAGE, "TestLater"]])

    def test_timing_write_failure_is_visible_without_changing_successful_execution(self):
        self.log_dir.mkdir()
        (self.log_dir / "runtime-phases.jsonl").mkdir()
        command, raw = child_command()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(stderr):
            result = shards.execute(self.repo, child_plan([command], ["TestChild"]), self.log_dir)
        self.assertEqual(result, 0)
        summary = json.loads((self.log_dir / "summary.json").read_text())
        self.assertEqual(summary["exit_code"], 0)
        self.assertEqual(summary["batch_exit_codes"], [0])
        self.assertIs(summary["timing_complete"], False)
        self.assertIn("WARN: runtime phase evidence unavailable", stderr.getvalue())
        self.assertNotIn(command[2], stderr.getvalue())
        self.assertEqual((self.log_dir / "events.jsonl").read_text(), raw)

    def test_spawn_failure_records_error_without_fabricating_an_exit_status(self):
        plan = child_plan([[str(self.root / "missing-executable")]], ["TestChild"])
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(OSError):
                shards.execute(self.repo, plan, self.log_dir)
        finish = next(row for row in self.records() if row["event"] == "finish")
        self.assertEqual(finish["status"], "error")
        self.assertIsNone(finish["exit_code"])
        self.assertFalse((self.log_dir / "summary.json").exists())

    def invoke_scope(self, discovery_failure=False):
        argv = ["pr_fast_scope.py", "--repo", str(self.repo), "--base", "a" * 40,
                "--head", "b" * 40, "--log-dir", str(self.log_dir)]
        calls = []

        def operation(label):
            calls.append(label)
            subprocess.run([sys.executable, "-c", "import time; time.sleep(0.01)"], check=True)

        def selected(*args):
            operation("scope")
            return [PACKAGE], "fixture"

        def discover(*args, **kwargs):
            operation("discovery")
            if discovery_failure:
                raise RuntimeError("controlled discovery failure")
            return {PACKAGE: ["TestChild"]}

        command, _ = child_command()
        plan = child_plan([command], ["TestChild"])
        with patch.object(sys, "argv", argv), patch.object(scope, "changed_paths", return_value=[]), \
             patch.object(scope, "behavior_scope", return_value=([], [], [])), \
             patch.object(scope.verification_scope, "matched", return_value=[]), \
             patch.object(scope, "resolve_packages", side_effect=selected), \
             patch.object(scope, "planning_inputs", return_value=({}, None)), \
             patch.object(scope, "discover", side_effect=discover), \
             patch.object(scope, "affected_test_plan", return_value=plan), \
             contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            result = scope.main()
        self.assertEqual(calls, ["scope", "discovery"])
        return result

    def test_scope_and_discovery_boundaries_wrap_original_calls_without_extra_children(self):
        self.assertEqual(self.invoke_scope(), 0)
        records = self.records()
        finished = {row["phase"]: row for row in records if row["event"] == "finish"}
        for phase in ("package_scope", "planning_inputs", "discovery", "plan"):
            self.assertEqual(finished[phase]["status"], "success")
            self.assertIsNone(finished[phase]["exit_code"])
            self.assertGreaterEqual(finished[phase]["elapsed_seconds"], 0)
        self.assertEqual(finished["batch"]["exit_code"], 0)
        self.assertLess(next(i for i, row in enumerate(records) if row["phase"] == "discovery"),
                        next(i for i, row in enumerate(records) if row["phase"] == "batch"))

    def test_discovery_error_is_not_test_success_and_does_not_start_batches(self):
        self.assertEqual(self.invoke_scope(discovery_failure=True), 1)
        records = self.records()
        finish = next(row for row in records if row["phase"] == "discovery" and row["event"] == "finish")
        self.assertEqual(finish["status"], "error")
        self.assertIsNone(finish["exit_code"])
        self.assertFalse(any(row["phase"] == "batch" for row in records))
        self.assertFalse((self.log_dir / "summary.json").exists())

    def test_empty_plan_does_not_invent_a_started_or_passed_batch(self):
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(shards.execute(self.repo, child_plan([], []), self.log_dir), 0)
        self.assertFalse(any(row["phase"] == "batch" for row in self.records()))
        summary = json.loads((self.log_dir / "summary.json").read_text())
        self.assertEqual(summary["batch_exit_codes"], [])
        self.assertEqual(summary["planned_process_batches"], 0)


if __name__ == "__main__":
    unittest.main()
