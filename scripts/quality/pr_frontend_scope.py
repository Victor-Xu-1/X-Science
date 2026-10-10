"""Build one bounded Vitest dependency-related command, never a global fallback."""
from pathlib import PurePosixPath


# These files have their own mandatory canonical provenance checks before this
# selector runs. Version-only manifests are filtered by pr_metadata_scope.
PROVENANCE = {"frontend/MIGRATION_MANIFEST.json", "frontend/THIRD_PARTY_LICENSES.json"}

# Static generated settings artwork is not a Vitest module. Bind it to the
# visual-contract tests that already require and size-budget those files.
SETTINGS_GENERATED_PREFIX = "frontend/public/branding/settings-generated-v3/"
SETTINGS_GENERATED_TESTS = ("tests/unit/settings/settingsVisualContract.test.ts",)

# Vite config is not under packages/. Bind preview cache policy edits to the
# unit test that imports productionPreviewCacheControl from vite.config.ts.
PREVIEW_CACHE_CONFIG = "frontend/vite.config.ts"
PREVIEW_CACHE_TESTS = ("tests/unit/build/viteProductionPreviewCache.test.ts",)


def related_command(paths: list[str]) -> list[str] | None:
    selected: set[str] = set()
    for value in paths:
        path = PurePosixPath(value)
        if path.is_absolute() or ".." in path.parts or "\\" in value or path.as_posix() != value:
            raise ValueError("frontend test input must be a normalized repository-relative path")
        if not value.startswith("frontend/") or value in PROVENANCE:
            continue
        if value.startswith(SETTINGS_GENERATED_PREFIX):
            selected.update(SETTINGS_GENERATED_TESTS)
            continue
        if value == PREVIEW_CACHE_CONFIG:
            selected.update(PREVIEW_CACHE_TESTS)
            continue
        if not value.startswith(("frontend/packages/", "frontend/tests/unit/",
                                 "frontend/tests/integration/", "frontend/tests/regression/")):
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
