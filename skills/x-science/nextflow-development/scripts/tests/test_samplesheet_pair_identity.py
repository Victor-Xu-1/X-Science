"""Exercise samplesheet path identity without reading any sequencing data."""

import csv
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "generate_samplesheet.py"


class SamplesheetPairIdentityTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.data = self.root / "input"
        self.sheet = self.root / "generated.csv"

    def add(self, folder, sample, mate, lane=1, chunk=1, suffix="fastq.gz"):
        path = self.data / folder / f"{sample}_L{lane:03d}_R{mate}_{chunk:03d}.{suffix}"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.touch()
        return str(path)

    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, "-B", str(SCRIPT), *map(str, args)],
            capture_output=True, text=True, encoding="utf-8", timeout=20,
        )

    def generate(self):
        return self.run_cli(self.data, "rnaseq", "-o", self.sheet, "--no-interactive")

    def rows(self):
        with self.sheet.open(newline="", encoding="utf-8") as handle:
            return list(csv.DictReader(handle))

    def test_two_runs_keep_all_paths_and_same_sample(self):
        expected = {
            (self.add(folder, "sample", 1), self.add(folder, "sample", 2))
            for folder in ("runA", "runB")
        }
        result = self.generate()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        rows = self.rows()
        self.assertEqual(len(rows), len(expected))
        self.assertEqual({(row["fastq_1"], row["fastq_2"]) for row in rows}, expected)
        self.assertEqual({row["sample"] for row in rows}, {"sample"})
        self.assertEqual(self.run_cli("--validate", self.sheet, "rnaseq").returncode, 0)

    def test_other_run_r1_stays_separate_from_existing_pair(self):
        r1 = self.add("runA", "sample", 1)
        r2 = self.add("runA", "sample", 2)
        orphan = self.add("runB", "sample", 1)
        result = self.generate()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(
            [(row["fastq_1"], row["fastq_2"]) for row in self.rows()],
            [(r1, r2), (orphan, "")],
        )
        self.assertIn(orphan, result.stdout)

    def test_unmatched_r2_fails_without_writing(self):
        self.add("runA", "sample", 1)
        self.add("runA", "sample", 2)
        orphan = self.add("runB", "sample", 2)
        result = self.generate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(orphan, result.stdout)
        self.assertFalse(self.sheet.exists())

    def test_same_directory_duplicate_mate_fails_without_writing(self):
        paths = [
            self.add("run", "sample", 1),
            self.add("run", "sample", 1, suffix="fq.gz"),
        ]
        self.add("run", "sample", 2)
        result = self.generate()
        self.assertNotEqual(result.returncode, 0)
        for path in paths:
            self.assertIn(path, result.stdout)
        self.assertFalse(self.sheet.exists())

    def test_explicit_nonstandard_chunks_must_match(self):
        for marker, separator in ((".R", "_"), (".R", "."), ("_", "_")):
            with self.subTest(marker=marker, separator=separator), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                r1 = root / f"sample{marker}1{separator}001.fastq.gz"
                wrong = root / f"sample{marker}2{separator}002.fastq.gz"
                right = root / f"sample{marker}2{separator}001.fastq.gz"
                r1.touch()
                wrong.touch()
                result = self.run_cli(root, "rnaseq", "-o", self.sheet, "--no-interactive")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(str(wrong), result.stdout)
                self.assertFalse(self.sheet.exists())

                wrong.rename(right)
                result = self.run_cli(root, "rnaseq", "-o", self.sheet, "--no-interactive")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(
                    [(row["fastq_1"], row["fastq_2"]) for row in self.rows()],
                    [(str(r1), str(right))],
                )
                self.sheet.unlink()

    def test_distinct_samples_lanes_and_chunks_are_not_collapsed(self):
        expected = {
            (self.add("run", sample, 1, lane, chunk), self.add("run", sample, 2, lane, chunk))
            for sample, lane, chunk in (("one", 1, 1), ("one", 2, 1), ("one", 2, 2), ("two", 1, 1))
        }
        result = self.generate()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(
            {(row["fastq_1"], row["fastq_2"]) for row in self.rows()}, expected
        )


if __name__ == "__main__":
    unittest.main()
