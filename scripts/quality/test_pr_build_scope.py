"""Real Make dry-runs prove default build and explicit full-test separation."""
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch
from scripts.quality import pr_build_scope as scope

ROOT = Path(__file__).resolve().parents[2]


class BuildDefaultTest(unittest.TestCase):
    def test_bare_make_builds_instead_of_running_global_tests(self):
        command = subprocess.check_output(['make', '--dry-run', '--no-print-directory'], cwd=ROOT, text=True)
        self.assertIn('go build', command)
        self.assertNotIn('go test', command)

    def test_explicit_test_target_stays_available_and_unchanged(self):
        command = subprocess.check_output(['make', '--dry-run', '--no-print-directory', 'test'], cwd=ROOT, text=True)
        self.assertEqual(command.strip(), 'go test ./...')

    def test_only_exact_default_addition_is_non_runtime(self):
        original = b'GO ?= go\ntest:\n\t$(GO) test ./...\nbuild:\n\t$(GO) build ./...\n'
        with patch.object(scope, 'snapshot', side_effect=[original, scope.DEFAULT + original]):
            self.assertTrue(scope.default_goal_only(ROOT, 'a' * 40, 'b' * 40))
        for changed in (original.replace(b'go', b'not-go'), scope.DEFAULT + original + b'\nunsafe:\n\ttrue\n'):
            with patch.object(scope, 'snapshot', side_effect=[original, changed]):
                self.assertFalse(scope.default_goal_only(ROOT, 'a' * 40, 'b' * 40))


if __name__ == '__main__':
    unittest.main()
