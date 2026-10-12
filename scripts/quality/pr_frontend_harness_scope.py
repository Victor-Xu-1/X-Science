"""Compile changed browser/real-test harnesses without executing their tasks."""
from pathlib import Path


CONFIGURATION = {"frontend/playwright.webui.config.ts", "frontend/vitest.config.ts"}


def harness_module(path: str) -> bool:
    return (path in CONFIGURATION or
            path.startswith("frontend/tests/web-e2e/") and path.endswith((".ts", ".tsx", ".js", ".mjs")) or
            path.startswith("frontend/tests/integration/") and path.endswith(".real.test.ts"))


def snapshot_asset(path: str) -> bool:
    return (path.startswith("frontend/tests/web-e2e/") and ".e2e.ts-snapshots/" in path
            and path.endswith(".png"))


def compile_command(paths: list[str], repo: Path) -> list[str] | None:
    entries = []
    frontend = (repo / "frontend").resolve()
    for path in paths:
        if not harness_module(path) or path.endswith((".fixture.tsx", ".fixture.html")):
            continue
        source = repo / path
        target = source.resolve()
        if target != source or not target.is_relative_to(frontend) or not target.is_file():
            raise ValueError("changed test harness requires its actual current source: " + path)
        entries.append(path.removeprefix("frontend/"))
    if not entries:
        return None
    # Esbuild parses the exact sources; imports, browser actions and real
    # backend/model calls are never evaluated. Product behavior stays covered
    # by its dependency-related tests and build, not claimed as real acceptance.
    script = """import { readFile } from 'node:fs/promises';
import { transform } from 'esbuild';
for (const file of process.argv.slice(1)) {
  const loader = file.endsWith('.tsx') ? 'tsx' : file.endsWith('.ts') ? 'ts' : 'js';
  await transform(await readFile(file, 'utf8'), { loader, sourcefile: file, target: 'es2022' });
}
console.log('Changed test harness syntax checked:', process.argv.length - 1);"""
    return ["node", "--input-type=module", "--eval", script, *sorted(set(entries))]
