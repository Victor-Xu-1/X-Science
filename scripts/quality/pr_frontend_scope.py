"""Bounded frontend import-graph tests and changed browser-fixture builds."""
from pathlib import Path, PurePosixPath


# These files have their own mandatory canonical provenance checks before this
# selector runs. Version-only manifests are filtered by pr_metadata_scope.
PROVENANCE = {"frontend/MIGRATION_MANIFEST.json", "frontend/THIRD_PARTY_LICENSES.json"}

# Static generated settings artwork is not a Vitest module. Bind it to the
# visual-contract tests that already require and size-budget those files.
SETTINGS_GENERATED_PREFIX = "frontend/public/branding/settings-generated-v3/"
SETTINGS_GENERATED_TESTS = ("tests/unit/settings/settingsVisualContract.test.ts",)


def _frontend_paths(paths: list[str]) -> list[str]:
    result = []
    for value in paths:
        path = PurePosixPath(value)
        if path.is_absolute() or ".." in path.parts or "\\" in value or path.as_posix() != value:
            raise ValueError("frontend test input must be a normalized repository-relative path")
        if not value.startswith("frontend/") or value in PROVENANCE:
            continue
        result.append(value)
    return result


def _browser_fixture(value: str) -> bool:
    return (value.startswith("frontend/tests/web-e2e/")
            and value.endswith((".fixture.html", ".fixture.tsx")))


def related_command(paths: list[str]) -> list[str] | None:
    selected: set[str] = set()
    for value in _frontend_paths(paths):
        # Browser entrypoints are compiled by fixture_command, not claimed as
        # Vitest coverage. Shared data retains its actual test import owners.
        if _browser_fixture(value):
            continue
        if value.startswith(SETTINGS_GENERATED_PREFIX):
            selected.update(SETTINGS_GENERATED_TESTS)
            continue
        if not value.startswith(("frontend/packages/", "frontend/tests/unit/",
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
const output = await mkdtemp(join(tmpdir(), 'x-science-pr-fixtures-'));
console.log('Changed browser fixture output:', output);
await build({
  configFile: resolve('vite.config.ts'), root: process.cwd(),
  build: { outDir: output, emptyOutDir: false,
    rollupOptions: { input: process.argv.slice(1).map(value => resolve(value)) } }
});"""
    # Match the existing product Vite build's heap budget, not Node's lower
    # default; this is a build process option, not a runtime/science limit.
    return ["node", "--max-old-space-size=4096", "--input-type=module", "--eval",
            build, *sorted(entries)]
