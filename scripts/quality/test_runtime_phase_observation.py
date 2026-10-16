"""Pure clock and machine-output boundaries for optional phase observations."""
import contextlib
from datetime import datetime, timezone
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.quality import pr_fast_scope as scope
from scripts.quality import runtime_phase_evidence as evidence


PHASE_INPUTS = [
    "scripts/quality/runtime_phase_evidence.py",
    "scripts/quality/test_runtime_phase_evidence.py",
    "scripts/quality/test_runtime_phase_observation.py",
]
PHASE_CHECK = [
    "python3", "-B", "-m", "unittest",
    "scripts.quality.test_runtime_phase_evidence",
    "scripts.quality.test_runtime_phase_observation",
]


class PhaseObservationBoundaryTests(unittest.TestCase):
    def test_phase_inputs_have_one_ci_only_owner_and_connected_checks(self):
        repo = Path(__file__).resolve().parents[2]
        entries = [{"ImportPath": "fixture/runtime", "Dir": str(repo / "internal/runtime")}]
        for path in PHASE_INPUTS:
            with self.subTest(path=path):
                selected, _ = scope.select_packages(repo, [path], entries)
                self.assertEqual(selected, [], "CI-only phase input must not broaden Go coverage")
                groups = scope.verification_scope.matched(repo, [path])
                self.assertEqual([group["name"] for group in groups], ["full-workflow-shard-contract"])
                self.assertFalse(groups[0]["frontend"])
                self.assertNotIn("packages", groups[0])
                self.assertEqual(scope.verification_scope.checks(groups), [
                    ["python3", "-B", "-m", "unittest", "scripts.quality.test_runtime_test_shards"],
                    PHASE_CHECK,
                ])

    def test_registered_phase_inputs_keep_unknown_mixed_and_embedded_go_ownership(self):
        repo = Path(__file__).resolve().parents[2]
        entries = [
            {"ImportPath": "fixture/root", "Dir": str(repo)},
            {"ImportPath": "fixture/client", "Dir": str(repo / "client"),
             "TestImports": ["fixture/root"]},
            {"ImportPath": "fixture/unrelated", "Dir": str(repo / "unrelated")},
        ]
        self.assertEqual(scope.select_packages(repo, PHASE_INPUTS, entries)[0], [])
        expected = ["fixture/client", "fixture/root"]
        self.assertEqual(scope.select_packages(repo, PHASE_INPUTS + ["root.go"], entries)[0], expected)
        all_packages = sorted(entry["ImportPath"] for entry in entries)
        unknown = PHASE_INPUTS[0] + ".unregistered"
        self.assertEqual(scope.verification_scope.matched(repo, [unknown]), [])
        for paths in [[unknown], PHASE_INPUTS + [unknown], PHASE_INPUTS + ["unknown/source.go"]]:
            with self.subTest(paths=paths):
                self.assertEqual(scope.select_packages(repo, paths, entries)[0], all_packages)
        entries[0]["EmbedFiles"] = [PHASE_INPUTS[0]]
        self.assertEqual(scope.select_packages(repo, PHASE_INPUTS, entries)[0], expected)

    def test_elapsed_uses_monotonic_clock_even_when_wall_clock_moves_backwards(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stderr(io.StringIO()):
            logger = evidence.RuntimePhaseEvidence(Path(directory))
            dates = [datetime(2026, 1, 2, tzinfo=timezone.utc), datetime(2026, 1, 1, tzinfo=timezone.utc)]
            with patch.object(evidence.time, "monotonic", side_effect=[100.0, 103.5]), \
                 patch.object(evidence, "datetime") as wall_clock:
                wall_clock.now.side_effect = dates
                with logger.measure("discovery", package_count=2):
                    pass
            rows = [json.loads(line) for line in logger.path.read_text().splitlines()]
        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[-1]["elapsed_seconds"], 3.5)
        self.assertGreater(rows[-1]["started_at"], rows[-1]["finished_at"])
        self.assertIsNone(rows[-1]["exit_code"])
        self.assertEqual(rows[-1]["status"], "success")

    def test_matrix_remains_single_json_stdout_without_timing_files_or_messages(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output, diagnostics = io.StringIO(), io.StringIO()
            argv = ["pr_fast_scope.py", "--repo", directory, "--base", "a" * 40,
                    "--head", "b" * 40, "--matrix", "--log-dir", str(root / "logs")]
            with patch.object(sys, "argv", argv), patch.object(scope, "changed_paths", return_value=[]), \
                 patch.object(scope, "behavior_scope", return_value=([], [], [])), \
                 patch.object(scope.verification_scope, "matched", return_value=[]), \
                 patch.object(scope, "resolve_packages", return_value=(["fixture/phase"], "fixture")), \
                 contextlib.redirect_stdout(output), contextlib.redirect_stderr(diagnostics):
                self.assertEqual(scope.main(), 0)
            self.assertEqual(json.loads(output.getvalue()), scope.pr_test_partition.job_matrix(["fixture/phase"]))
            self.assertEqual(diagnostics.getvalue(), "")
            self.assertFalse((root / "logs").exists())


if __name__ == "__main__":
    unittest.main()
