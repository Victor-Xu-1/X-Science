"""Focused regressions for the existing CI's frontend test selection."""
import unittest
from scripts.quality.pr_frontend_scope import related_command


class FrontendRelatedScopeTests(unittest.TestCase):
    def test_exact_source_css_and_changed_tests_are_related_inputs(self):
        command = related_command([
            "frontend/packages/desktop/src/viewer.tsx",
            "frontend/packages/desktop/src/viewer.css",
            "frontend/tests/unit/viewer.dom.test.tsx",
            "frontend/packages/desktop/src/viewer.tsx",
        ])
        self.assertEqual(command, ["npx", "--no-install", "vitest", "related", "--run",
                                  "--project", "node", "--project", "dom",
                                  "packages/desktop/src/viewer.css",
                                  "packages/desktop/src/viewer.tsx",
                                  "tests/unit/viewer.dom.test.tsx"])
        self.assertNotIn("--passWithNoTests", command)
        self.assertNotIn("--retry", command)

    def test_provenance_and_non_frontend_inputs_do_not_authorize_global_tests(self):
        self.assertIsNone(related_command(["docs/modules/science.md",
                                         "frontend/MIGRATION_MANIFEST.json",
                                         "frontend/THIRD_PARTY_LICENSES.json",
                                         "scripts/quality/pr_frontend_scope.py"]))

    def test_unknown_configuration_or_dependency_scope_fails_closed(self):
        for path in ["frontend/package-lock.json", "frontend/package.json", "frontend/vitest.config.ts",
                     "frontend/tests/vitest.dom.setup.ts"]:
            with self.subTest(path=path), self.assertRaisesRegex(ValueError, "ownership is not known"):
                related_command(["frontend/packages/desktop/src/viewer.tsx", path])

    def test_unsafe_paths_are_rejected(self):
        for path in ["/frontend/viewer.tsx", "frontend/../secret.ts", "frontend\\viewer.tsx", "frontend//viewer.tsx"]:
            with self.subTest(path=path), self.assertRaises(ValueError):
                related_command([path])

    def test_real_integration_scope_is_not_implicitly_executed(self):
        with self.assertRaisesRegex(ValueError, "separately authorized"):
            related_command(["frontend/tests/integration/remote.real.test.ts"])


if __name__ == "__main__":
    unittest.main()
