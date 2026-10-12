"""Focused regressions for the existing CI's frontend test selection."""
import json
import os
import subprocess
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from scripts.quality.pr_frontend_scope import PROVENANCE, fixture_command, harness_command, related_command


# Execute the emitted command without installing Vite or building a module graph.
# This contract double covers Vite's string/array/object input merge and supplies
# the resolved global/client inputs to the real command's validation hooks.
VITE_BUILD_CONTRACT = """import { writeFileSync } from 'node:fs';
export async function build(config) {
  const scenario = JSON.parse(process.env.FIXTURE_SCOPE_CASE);
  const requested = config.build.rollupOptions.input;
  const product = '/production/index.html';
  const defaults = { string: product, array: [product], object: { product } };
  const original = defaults[scenario.defaultInput];
  const input = Array.isArray(original) || Array.isArray(requested)
    ? [original, requested].flat()
    : typeof original === 'object' ? { ...original, ...requested } : requested;
  const resolved = {
    build: { ...config.build, rollupOptions: { input } },
    environments: { client: { build: { rollupOptions: { input } } } }
  };
  const report = {
    input, root: config.root, configFile: config.configFile, buildStarted: false,
    globalAliasesRequested: resolved.build.rollupOptions.input === requested,
    clientAliasesRequested: resolved.environments.client.build.rollupOptions.input === requested,
    scopeOptionsOrder: config.plugins?.find(plugin => plugin.name === 'changed-browser-fixture-input-scope')?.options?.order
  };
  const target = scenario.target === 'client' ? resolved.environments.client : resolved;
  if (scenario.corruption === 'extra') target.build.rollupOptions.input = { ...input, product };
  if (scenario.corruption === 'missing') target.build.rollupOptions.input = {};
  if (scenario.corruption === 'duplicate') target.build.rollupOptions.input = {
    ...input, duplicate: Object.values(input)[0]
  };
  if (scenario.corruption === 'nonstring') target.build.rollupOptions.input = { 'fixture-0': [product] };
  if (scenario.corruption === 'replaced') target.build.rollupOptions.input = { ...input, 'fixture-0': product };
  if (scenario.corruption === 'no-client') delete resolved.environments.client;
  const mutateInput = () => {
    if (scenario.corruption.endsWith('in-place-add')) target.build.rollupOptions.input.product = product;
    if (scenario.corruption.endsWith('in-place-delete')) delete target.build.rollupOptions.input['fixture-0'];
    if (scenario.corruption.endsWith('in-place-replace')) target.build.rollupOptions.input['fixture-0'] = product;
  };
  try {
    if (scenario.corruption?.startsWith('in-place-')) mutateInput();
    for (const plugin of config.plugins ?? []) await plugin.configResolved?.(resolved);
    if (scenario.corruption?.startsWith('late-in-place-')) mutateInput();
    if (scenario.corruption === 'late-extra') target.build.rollupOptions.input = { ...input, product };
    const options = { input: scenario.corruption === 'rollup-extra' ? { ...input, product } : input };
    const productPlugin = { options: { order: 'post', handler(options) {
      if (scenario.corruption === 'post-options-in-place-add') options.input.product = product;
    } } };
    const hookOrder = { pre: -1, post: 1 };
    const hooks = [productPlugin, ...(config.plugins ?? [])].map(plugin => plugin.options).filter(Boolean);
    hooks.sort((left, right) => (hookOrder[left.order] ?? 0) - (hookOrder[right.order] ?? 0));
    for (const hook of hooks) await (typeof hook === 'function' ? hook : hook.handler)(options);
    report.buildStarted = true;
  } finally {
    writeFileSync(process.env.FIXTURE_SCOPE_REPORT, JSON.stringify(report));
  }
}
"""


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

    def test_shared_and_browser_fixtures_use_distinct_bounded_checks(self):
        paths = [
            "frontend/tests/fixtures/structureLigands.ts",
            "frontend/tests/web-e2e/structureLigandSelection.fixture.tsx",
            "frontend/tests/web-e2e/structureLigandSelection.fixture.html",
            "frontend/tests/unit/previews/structureLigandSelection.test.ts",
        ]
        command = related_command(paths)
        self.assertEqual(command, ["npx", "--no-install", "vitest", "related", "--run",
                                  "--project", "node", "--project", "dom",
                                  "tests/fixtures/structureLigands.ts",
                                  "tests/unit/previews/structureLigandSelection.test.ts"])
        fixtures = fixture_command(paths, Path(__file__).resolve().parents[2])
        self.assertEqual(fixtures[5:], ["tests/web-e2e/structureLigandSelection.fixture.html"])
        self.assertNotIn("--passWithNoTests", command)
        self.assertNotIn("--retry", command)

    def test_provenance_and_non_frontend_inputs_do_not_authorize_global_tests(self):
        for path in sorted(PROVENANCE):
            self.assertIsNone(related_command([path]))
            self.assertIsNone(fixture_command([path], Path(__file__).resolve().parents[2]))
        self.assertIsNone(related_command(["docs/modules/science.md",
                                         "scripts/quality/pr_frontend_scope.py"]))

    def test_shared_fixture_data_uses_the_vitest_import_graph(self):
        command = related_command(["frontend/tests/fixtures/ligands.ts"])
        self.assertEqual(command[-1], "tests/fixtures/ligands.ts")
        self.assertEqual(command[2:5], ["vitest", "related", "--run"])

    def test_readme_is_documentation_not_an_unowned_runtime_module(self):
        self.assertIsNone(related_command(["frontend/README.md"]))
        command = related_command(["frontend/README.md", "frontend/packages/desktop/src/viewer.tsx"])
        self.assertEqual(command[-1], "packages/desktop/src/viewer.tsx")
        self.assertNotIn("README.md", command)

    def test_build_modules_use_import_owners_without_a_global_fallback(self):
        command = related_command(["frontend/scripts/exampleTransform.ts", "frontend/vite.config.ts"])
        self.assertEqual(command[9:], ["scripts/exampleTransform.ts", "vite.config.ts"])
        self.assertNotIn("--passWithNoTests", command)
        with self.assertRaisesRegex(ValueError, "ownership is not known"):
            related_command(["frontend/scripts/unowned.sh"])

    def test_browser_fixture_is_bundled_once_not_misreported_as_a_unit_test(self):
        with TemporaryDirectory() as directory:
            repo = Path(directory)
            html = repo / "frontend/tests/web-e2e/example.fixture.html"
            html.parent.mkdir(parents=True)
            html.write_text('<script type="module" src="./example.fixture.tsx"></script>')
            paths = [str(html.relative_to(repo)),
                     "frontend/tests/web-e2e/example.fixture.tsx"]
            self.assertIsNone(related_command(paths))
            command = fixture_command(paths, repo)
            self.assertEqual(command[:4], ["node", "--max-old-space-size=4096", "--input-type=module", "--eval"])
            self.assertEqual(command[5:], ["tests/web-e2e/example.fixture.html"])
            self.assertIn("emptyOutDir: false", command[4])
            self.assertIsNone(fixture_command(["frontend/tests/fixtures/ligands.ts"], repo))

    def test_missing_or_external_browser_fixture_entry_fails_closed(self):
        with TemporaryDirectory() as directory:
            repo = Path(directory)
            path = "frontend/tests/web-e2e/example.fixture.tsx"
            with self.assertRaisesRegex(ValueError, "HTML entry"):
                fixture_command([path], repo)
            frontend = repo / "frontend"
            (frontend / "tests").mkdir(parents=True)
            outside = repo / "outside.fixture.html"
            outside.write_text("fixture")
            (frontend / "tests/web-e2e").symlink_to(repo, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "HTML entry"):
                fixture_command(["frontend/tests/web-e2e/outside.fixture.html"], repo)

    def run_fixture_contract(self, scenario):
        with TemporaryDirectory() as directory:
            repo = Path(directory)
            entries = ["tests/web-e2e/a/example.fixture.html", "tests/web-e2e/b/example.fixture.html"]
            for entry in entries:
                html = repo / "frontend" / entry
                html.parent.mkdir(parents=True, exist_ok=True)
                html.write_text("fixture")
            vite = repo / "frontend/node_modules/vite"
            vite.mkdir(parents=True)
            (vite / "package.json").write_text('{"type":"module","exports":"./index.js"}')
            (vite / "index.js").write_text(VITE_BUILD_CONTRACT)
            report = repo / "report.json"
            paths = ["frontend/" + entry for entry in reversed(entries)]
            paths.append("frontend/tests/web-e2e/a/example.fixture.tsx")
            command = fixture_command(paths, repo)
            completed = subprocess.run(command, cwd=repo / "frontend", text=True,
                                       capture_output=True, timeout=30,
                                       env={**os.environ, "FIXTURE_SCOPE_CASE": json.dumps(scenario),
                                            "FIXTURE_SCOPE_REPORT": str(report),
                                            "TMPDIR": directory, "TEMP": directory, "TMP": directory})
            return completed, json.loads(report.read_text()), {
                "fixture-" + str(index): str(repo / "frontend" / entry)
                for index, entry in enumerate(entries)
            }

    def test_resolved_fixture_inputs_replace_product_string_without_basename_collision(self):
        completed, report, expected = self.run_fixture_contract({"defaultInput": "string"})
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(report["input"], expected)
        self.assertTrue(report["buildStarted"])
        self.assertEqual(report["configFile"], str(Path(report["root"]) / "vite.config.ts"))

    def test_future_product_array_or_object_inputs_fail_before_build(self):
        for default in ["array", "object"]:
            with self.subTest(default=default):
                completed, report, _ = self.run_fixture_contract({"defaultInput": default})
                self.assertNotEqual(completed.returncode, 0)
                self.assertIn("browser fixture input scope", completed.stderr)
                self.assertFalse(report["buildStarted"])

    def test_global_and_client_inputs_each_require_exact_owned_fixture_map(self):
        for target in ["global", "client"]:
            for corruption in ["extra", "missing", "duplicate", "nonstring", "replaced", "late-extra"]:
                with self.subTest(target=target, corruption=corruption):
                    completed, report, _ = self.run_fixture_contract({
                        "defaultInput": "string", "target": target, "corruption": corruption,
                    })
                    self.assertNotEqual(completed.returncode, 0)
                    self.assertIn("browser fixture input scope", completed.stderr)
                    self.assertFalse(report["buildStarted"])
        completed, report, _ = self.run_fixture_contract({
            "defaultInput": "string", "corruption": "no-client",
        })
        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("browser fixture input scope", completed.stderr)
        self.assertFalse(report["buildStarted"])

    def test_actual_rollup_input_is_rechecked_before_the_module_graph(self):
        completed, report, _ = self.run_fixture_contract({
            "defaultInput": "string", "corruption": "rollup-extra",
        })
        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("browser fixture input scope changed in Rollup build", completed.stderr)
        self.assertFalse(report["buildStarted"])

    def assert_in_place_mutations_rejected(self, prefix):
        for target in ["global", "client"]:
            for mutation in ["add", "delete", "replace"]:
                with self.subTest(target=target, mutation=mutation, prefix=prefix):
                    completed, report, _ = self.run_fixture_contract({
                        "defaultInput": "string", "target": target,
                        "corruption": prefix + "in-place-" + mutation,
                    })
                    self.assertTrue(report["globalAliasesRequested"])
                    self.assertTrue(report["clientAliasesRequested"])
                    self.assertNotEqual(completed.returncode, 0)
                    self.assertIn("browser fixture input scope", completed.stderr)
                    self.assertFalse(report["buildStarted"])

    def test_in_place_config_input_mutations_fail_before_the_module_graph(self):
        self.assert_in_place_mutations_rejected("")

    def test_in_place_late_input_mutations_fail_before_the_module_graph(self):
        self.assert_in_place_mutations_rejected("late-")

    def test_post_order_options_mutation_is_checked_before_the_module_graph(self):
        completed, report, _ = self.run_fixture_contract({
            "defaultInput": "string", "corruption": "post-options-in-place-add",
        })
        self.assertEqual(report["scopeOptionsOrder"], "post")
        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("browser fixture input scope", completed.stderr)
        self.assertFalse(report["buildStarted"])

    def test_public_branding_assets_bind_existing_identity_and_visual_tests(self):
        command = related_command([
            "frontend/public/branding/settings-generated-v3/artwork/account-network.webp",
            "frontend/packages/desktop/src/renderer/pages/settings/components/SettingsGeneratedAsset.tsx",
        ])
        self.assertEqual(command[:6], ["npx", "--no-install", "vitest", "related", "--run",
                                       "--project"])
        self.assertIn("tests/unit/settings/settingsVisualContract.test.ts", command)
        self.assertIn("tests/unit/renderer/XScienceIdentity.test.ts", command)
        self.assertIn(
            "packages/desktop/src/renderer/pages/settings/components/SettingsGeneratedAsset.tsx",
            command,
        )
        self.assertNotIn("public/branding/settings-generated-v3/artwork/account-network.webp", command)
        self.assertNotIn("--passWithNoTests", command)

    def test_unknown_configuration_or_dependency_scope_fails_closed(self):
        for path in ["frontend/unknown-lock.json", "frontend/tests/vitest.dom.setup.ts",
                     "frontend/public/unknown-runtime.js"]:
            with self.subTest(path=path), self.assertRaisesRegex(ValueError, "ownership is not known"):
                related_command(["frontend/packages/desktop/src/viewer.tsx", path])

    def test_locked_dependency_authority_keeps_build_checks_not_documentation(self):
        from scripts.quality.pr_fast_scope import documentation_only, frontend_affected
        paths = ["frontend/package.json", "frontend/package-lock.json"]
        self.assertTrue(frontend_affected(paths))
        for path in paths:
            self.assertFalse(documentation_only(path))
            self.assertIsNone(related_command([path]))
        command = related_command([*paths, "frontend/packages/desktop/src/viewer.tsx"])
        self.assertEqual(command[9:], ["packages/desktop/src/viewer.tsx"])

    def test_unsafe_paths_are_rejected(self):
        for path in ["/frontend/viewer.tsx", "frontend/../secret.ts", "frontend\\viewer.tsx", "frontend//viewer.tsx"]:
            with self.subTest(path=path), self.assertRaises(ValueError):
                related_command([path])

    def test_real_integration_scope_is_not_implicitly_executed(self):
        with TemporaryDirectory() as directory:
            repo = Path(directory)
            path = "frontend/tests/integration/remote.real.test.ts"
            source = repo / path
            source.parent.mkdir(parents=True)
            source.write_text("throw new Error('must never execute a real task');")
            self.assertIsNone(related_command([path]))
            command = harness_command([path], repo)
            self.assertEqual(command[:3], ["node", "--input-type=module", "--eval"])
            self.assertIn("transform(", command[3])
            self.assertNotIn("eval(", command[3])
            self.assertEqual(command[4:], [path.removeprefix("frontend/")])
            self.assertNotIn("vitest", command)
            self.assertNotIn("playwright", command)

    def test_missing_or_linked_test_harness_source_is_rejected(self):
        with TemporaryDirectory() as directory:
            repo = Path(directory)
            path = "frontend/tests/web-e2e/example.e2e.ts"
            with self.assertRaisesRegex(ValueError, "actual current source"):
                harness_command([path], repo)
            target = repo / path
            target.parent.mkdir(parents=True)
            outside = repo / "outside.ts"
            outside.write_text("throw new Error('outside');")
            target.symlink_to(outside)
            with self.assertRaisesRegex(ValueError, "actual current source"):
                harness_command([path], repo)

    def test_static_runtime_scripts_keep_their_actual_behavior_test_owners(self):
        command = related_command(["frontend/public/theme-init.js", "frontend/public/storage-namespace-upgrade.js"])
        self.assertIn("tests/unit/themeVisualContract.test.ts", command)
        self.assertIn("tests/unit/renderer/storageNamespaceUpgrade.test.ts", command)
        self.assertNotIn("--passWithNoTests", command)


if __name__ == "__main__":
    unittest.main()
