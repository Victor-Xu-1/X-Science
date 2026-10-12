"""Bounded frontend import-graph tests and explicitly owned browser fixtures."""
from pathlib import Path, PurePosixPath


# These files have their own mandatory canonical provenance checks before this
# selector runs. Version-only manifests are filtered by pr_metadata_scope.
PROVENANCE = {"frontend/MIGRATION_MANIFEST.json", "frontend/THIRD_PARTY_LICENSES.json"}

# Static branding files are not Vitest modules. Bind them to the existing
# identity/visual tests that already hash and require those assets.
BRANDING_PREFIX = "frontend/public/branding/"
BRANDING_TESTS = (
    "tests/unit/settings/settingsVisualContract.test.ts",
    "tests/unit/renderer/XScienceIdentity.test.ts",
    "tests/unit/renderer/ProductBrand.dom.test.tsx",
    "tests/unit/renderer/SynonBiomedBranding.test.ts",
)

# This is one synthetic HTTP/SQLite browser fixture, not the authenticated
# scientific acceptance workflow. Unknown browser drivers remain unowned.
ARTIFACT_LINK_BROWSER_DRIVER = "frontend/tests/web-e2e/artifactLinks.browser.mjs"
ARTIFACT_LINK_BROWSER_INPUTS = frozenset({
    ARTIFACT_LINK_BROWSER_DRIVER,
    "frontend/tests/web-e2e/artifactLinks.fixture.tsx",
    "frontend/tests/web-e2e/artifactLinks.fixture.html",
    "internal/server/artifact_link_browser_test.go",
})


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
        if value.startswith(BRANDING_PREFIX):
            selected.update(BRANDING_TESTS)
            continue
        # Browser entrypoints are compiled by fixture_command, not claimed as
        # Vitest coverage. Shared data retains its actual test import owners.
        if _browser_fixture(value):
            continue
        if value == ARTIFACT_LINK_BROWSER_DRIVER:
            # browser_command owns this exact driver. Do not claim it as a
            # Vitest input or allow unrelated browser drivers through.
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


def browser_command(paths: list[str], repo: Path, evidence: Path | None = None) -> list[str] | None:
    # Validate all changed paths, including the non-frontend Go owner, before
    # matching the one exact fixture. A missing owner cannot silently skip it.
    _frontend_paths(paths)
    if not ARTIFACT_LINK_BROWSER_INPUTS.intersection(paths):
        return None
    root = repo.resolve()
    frontend = (root / "frontend").resolve()
    for value in sorted(ARTIFACT_LINK_BROWSER_INPUTS):
        path = root / value
        resolved = path.resolve()
        owner_root = frontend if value.startswith("frontend/") else root
        if not resolved.is_relative_to(owner_root) or not resolved.is_file():
            raise ValueError("artifact-link browser requires an owned file: " + value)
        if any(parent.is_symlink() for parent in (path, *path.parents) if parent != root and root in parent.parents):
            raise ValueError("artifact-link browser owner cannot be a symlink: " + value)
    command = ["env", "SYNON_ARTIFACT_LINK_BROWSER=1"]
    if evidence is not None:
        directory = evidence.resolve()
        if directory == root or root in directory.parents:
            raise ValueError("browser test evidence must be outside source")
        command.append("SYNON_ARTIFACT_LINK_EVIDENCE=" + str(directory))
    return [*command, "go", "test", "-buildvcs=false", "./internal/server",
            "-run", "^TestArtifactHashLinkBrowser$", "-count=1", "-timeout=180s", "-json"]


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
