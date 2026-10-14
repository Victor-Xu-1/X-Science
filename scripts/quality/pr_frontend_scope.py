"""Bounded frontend import-graph tests and changed browser-fixture builds."""
from pathlib import Path, PurePosixPath

if __package__:
    from . import pr_frontend_harness_scope as harness
else:
    import pr_frontend_harness_scope as harness


# These files have their own mandatory canonical provenance checks before this
# selector runs. Version-only manifests are filtered by pr_metadata_scope.
PROVENANCE = {"frontend/MIGRATION_MANIFEST.json", "frontend/THIRD_PARTY_LICENSES.json"}
DOCUMENTATION = {"frontend/README.md"}
# npm's locked install and the existing type/lint/product build own these
# manifests. They are never documentation or executable Vitest modules.
BUILD_INPUTS = {"frontend/package.json", "frontend/package-lock.json"}

# Static branding files are not Vitest modules. Bind them to the existing
# identity/visual tests that already hash and require those assets.
BRANDING_PREFIX = "frontend/public/branding/"
BRANDING_TESTS = (
    "tests/unit/settings/settingsVisualContract.test.ts",
    "tests/unit/renderer/XScienceIdentity.test.ts",
    "tests/unit/renderer/ProductBrand.dom.test.tsx",
    "tests/unit/renderer/XScienceBranding.test.ts",
)

STATIC_RUNTIME_TESTS = {
    "frontend/public/theme-init.js": ("tests/unit/themeVisualContract.test.ts",),
    "frontend/public/storage-namespace-upgrade.js": (
        "tests/unit/renderer/storageNamespaceUpgrade.test.ts",
        "tests/unit/renderer/storageNamespaceCaches.test.ts",
    ),
    "frontend/public/sw.js": ("tests/unit/pwa/serviceWorkerActivation.test.ts",),
    "frontend/public/dev-sw-cleanup.js": ("tests/unit/pwa/serviceWorkerActivation.test.ts",),
}


def _frontend_paths(paths: list[str]) -> list[str]:
    result = []
    for value in paths:
        path = PurePosixPath(value)
        if path.is_absolute() or ".." in path.parts or "\\" in value or path.as_posix() != value:
            raise ValueError("frontend test input must be a normalized repository-relative path")
        if not value.startswith("frontend/") or value in PROVENANCE or value in DOCUMENTATION:
            continue
        result.append(value)
    return result


def _browser_fixture(value: str) -> bool:
    return (value.startswith("frontend/tests/web-e2e/")
            and value.endswith((".fixture.html", ".fixture.tsx")))


def related_command(paths: list[str]) -> list[str] | None:
    selected: set[str] = set()
    for value in _frontend_paths(paths):
        if value in BUILD_INPUTS:
            continue
        if value.startswith(BRANDING_PREFIX):
            selected.update(BRANDING_TESTS)
            continue
        if value in STATIC_RUNTIME_TESTS:
            selected.update(STATIC_RUNTIME_TESTS[value])
            continue
        if harness.harness_module(value) or harness.snapshot_asset(value):
            continue
        # Browser entrypoints are compiled by fixture_command, not claimed as
        # Vitest coverage. Shared data retains its actual test import owners.
        if _browser_fixture(value):
            continue
        build_module = (value == "frontend/vite.config.ts" or
                        (value.startswith("frontend/scripts/") and value.endswith((".ts", ".js", ".mjs"))))
        # Build modules have normal import owners in the focused test graph;
        # the existing product build also checks their emitted application.
        # Runner configuration/dependencies still cannot imply a global suite.
        if not build_module and not value.startswith(("frontend/packages/", "frontend/tests/unit/",
                                 "frontend/tests/integration/", "frontend/tests/regression/",
                                 "frontend/tests/fixtures/")):
            raise ValueError("frontend test ownership is not known for " + value +
                             "; bind focused coverage instead of running a global suite")
        if value.endswith(".real.test.ts"):
            raise ValueError("real integration tests require their separately authorized workflow")
        selected.add(value.removeprefix("frontend/"))
    if not selected:
        return None
    # Vitest/Vite own import and mock resolution. Passing exact changed test
    # files also retains direct test edits; no target/task/file-to-test table.
    # No passWithNoTests or retry flag: unknown/empty coverage is a failed gate.
    return ["npx", "--no-install", "vitest", "related", "--run",
            "--project", "node", "--project", "dom", *sorted(selected)]


def harness_command(paths: list[str], repo: Path) -> list[str] | None:
    return harness.compile_command(_frontend_paths(paths), repo)


def snapshot_inputs(paths: list[str]) -> list[str]:
    return [path for path in _frontend_paths(paths) if harness.snapshot_asset(path)]


def fixture_command(paths: list[str], repo: Path) -> list[str] | None:
    entries: set[str] = set()
    frontend = (repo / "frontend").resolve()
    for value in _frontend_paths(paths):
        if not _browser_fixture(value):
            continue
        entry = PurePosixPath(value).with_suffix(".html")
        resolved = (repo / str(entry)).resolve()
        if not resolved.is_relative_to(frontend) or not resolved.is_file():
            raise ValueError("browser fixture requires an owned HTML entry: " + str(entry))
        entries.add(str(entry).removeprefix("frontend/"))
    if not entries:
        return None
    # Use the same locked Vite config as the product. Arguments are literal,
    # normalized entry paths, not shell text. No browser/scientific task starts,
    # no wildcard entries or global suite, and failed builds propagate normally.
    # Output stays in a fresh external directory; never empty a source path.
    build = """import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { build } from 'vite';
// An array is concatenated with the product's input by Vite's config merge.
// Stable names replace a string input and keep same-basename entries distinct.
const expectedInput = Object.freeze(Object.fromEntries(process.argv.slice(1).map((value, index) =>
  ['fixture-' + index, resolve(value)])));
// Vite aliases the input into resolved configs. Never share the expected map.
const input = { ...expectedInput };
function assertFixtureInput(actual, label) {
  if (!actual || typeof actual !== 'object' || Array.isArray(actual) ||
      Object.keys(actual).length !== Object.keys(expectedInput).length ||
      Object.entries(expectedInput).some(([name, path]) =>
        !Object.hasOwn(actual, name) || actual[name] !== path)) {
    throw new Error('browser fixture input scope changed in ' + label);
  }
}
function assertFixtureConfig(config) {
  assertFixtureInput(config?.build?.rollupOptions?.input, 'resolved build');
  assertFixtureInput(config?.environments?.client?.build?.rollupOptions?.input, 'resolved client');
}
let resolvedConfig;
const fixtureScope = {
  name: 'changed-browser-fixture-input-scope', apply: 'build', enforce: 'post',
  configResolved(config) {
    resolvedConfig = config;
    assertFixtureConfig(config);
  },
  options: {
    order: 'post',
    handler(options) {
      // Recheck after all config hooks, before Rollup starts the module graph.
      assertFixtureConfig(resolvedConfig);
      assertFixtureInput(options.input, 'Rollup build');
    }
  }
};
const output = await mkdtemp(join(tmpdir(), 'x-science-pr-fixtures-'));
console.log('Changed browser fixture output:', output);
await build({
  configFile: resolve('vite.config.ts'), root: process.cwd(),
  plugins: [fixtureScope],
  build: { outDir: output, emptyOutDir: false,
    rollupOptions: { input } }
});"""
    # Match the existing product Vite build's heap budget, not Node's lower
    # default; this is a build process option, not a runtime/science limit.
    return ["node", "--max-old-space-size=4096", "--input-type=module", "--eval",
            build, *sorted(entries)]
