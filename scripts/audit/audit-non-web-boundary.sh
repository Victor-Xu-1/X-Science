#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}
ROOT_DIR=$(cd "$ROOT_DIR" && pwd -P)
BOUNDARY="$ROOT_DIR/docs/non-web-asset-boundary.json"

if [[ ! -f "$BOUNDARY" || -L "$BOUNDARY" ]]; then
  echo "ERROR: non-Web asset boundary manifest is missing or is a symbolic link" >&2
  exit 1
fi

expected_web_files=$(python3 - "$BOUNDARY" <<'PY'
import json
import sys
from pathlib import Path

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as handle:
    document = json.load(handle)
expected = [
    "docs/licenses/frontend-dependencies/jszip@3.10.1/lib/license_header.js",
    "skills/x-science/skill-creator/assets/eval_review.html",
    "skills/x-science/skill-creator/eval-viewer/viewer.html",
    "assets/x-science-link/x-science-browser-extension-v0.6.11.zip",
    "assets/optional/mcp-servers/ketcher-chemistry/widget/index.html.gz",
]
actual = [item.get("path") for item in document.get("allowedVisualAssets", [])]
expected_tooling = [
    "scripts/conda-lock-source.mjs",
    "scripts/conda-lock-source.test.mjs",
    "scripts/audit/reference_harness_inventory.mjs",
    "scripts/dev/frontend-dependency-watch.mjs",
    "scripts/generate-conda-runtime-lock.mjs",
    "scripts/generate-conda-runtime-lock.test.mjs",
    "scripts/packaging/test_connector_namespace.mjs",
]
expected_connector = ["assets/x-science-link/source/" + name for name in (
    "INSTALL.html", "approval.html", "approval.js", "background-download-guard.test.ts",
    "background-tab-groups.test.ts", "background-url.test.mjs", "background.js", "content-script.js",
    "icons/x-science-mark.png", "link-runtime.js", "link-runtime.test.ts",
    "local-files-database.js", "manifest.json", "options.html", "options.js",
    "permission-snapshot.js", "personalization.html", "personalization.js",
    "popup-ux.test.ts", "popup.html", "popup.js", "styles.css", "workspace.html", "workspace.js",
)]
actual_tooling = [item.get("path") for item in document.get("allowedToolingFiles", [])]
actual_connector = document.get("allowedConnectorSources", [])
if (document.get("schemaVersion") != 1 or actual != expected or actual_tooling != expected_tooling
        or actual_connector != expected_connector):
    raise SystemExit("ERROR: non-Web asset boundary manifest does not match the audited allowlist")
root = Path(path).resolve().parents[1]
connector = root / "assets/x-science-link/source"
if connector.is_symlink() or not connector.is_dir():
    raise SystemExit("ERROR: connector source must be a real directory")
observed = []
for entry in connector.rglob("*"):
    if entry.is_symlink() or (not entry.is_dir() and not entry.is_file()):
        raise SystemExit("ERROR: connector source contains a link or special file")
    if entry.is_file():
        observed.append(entry.relative_to(root).as_posix())
if sorted(observed) != expected_connector:
    raise SystemExit("ERROR: connector source does not match the exact audited inventory")
web_extensions = (".html", ".htm", ".css", ".scss", ".sass", ".less", ".js", ".mjs", ".cjs",
                  ".jsx", ".ts", ".tsx", ".vue", ".svelte", ".astro")
print("\n".join(sorted(path for path in expected + expected_tooling + expected_connector
                       if path.endswith(web_extensions))))
PY
)

for required_frontend_file in \
  frontend/package.json \
  frontend/SOURCE_IMPORT_MANIFEST.json \
  frontend/MIGRATION_MANIFEST.json; do
  if [[ ! -f "$ROOT_DIR/$required_frontend_file" || -L "$ROOT_DIR/$required_frontend_file" ]]; then
    echo "ERROR: the sole product frontend is missing $required_frontend_file" >&2
    exit 1
  fi
done

# The product Web tree is audited separately from all standalone visual assets.
actual_non_product_web_files=$(find "$ROOT_DIR" \
  -path "$ROOT_DIR/.git" -prune -o \
  -path "$ROOT_DIR/frontend" -prune -o \
  -type d \( \
    -name node_modules -o -name out -o -name coverage -o \
    -name test-results -o -name playwright-report -o -name dist \
  \) -prune -o \
  -type f \( \
    -iname '*.html' -o -iname '*.htm' -o -iname '*.css' \
    -o -iname '*.scss' -o -iname '*.sass' -o -iname '*.less' \
    -o -iname '*.js' -o -iname '*.mjs' -o -iname '*.cjs' \
    -o -iname '*.jsx' -o -iname '*.ts' -o -iname '*.tsx' \
    -o -iname '*.vue' -o -iname '*.svelte' -o -iname '*.astro' \
  \) -printf '%P\n' | LC_ALL=C sort)
if [[ "$actual_non_product_web_files" != "$expected_web_files" ]]; then
  echo "ERROR: source contains unclassified Web-like files outside frontend/" >&2
  diff -u <(printf '%s\n' "$expected_web_files") <(printf '%s\n' "$actual_non_product_web_files") >&2 || true
  exit 1
fi

# A license header has a source-like extension but is not executable source.
# Classify only the exact inventoried file, and fail if code is added to it.
python3 - "$ROOT_DIR/docs/licenses/frontend-dependencies/jszip@3.10.1/lib/license_header.js" <<'PY'
import pathlib
import re
import sys
text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
if not re.fullmatch(r"\s*/\*(?:(?!\*/).)*\*/\s*", text, flags=re.S):
    raise SystemExit("ERROR: license header contains executable content")
PY

banned_directories=$(find "$ROOT_DIR" -mindepth 1 -maxdepth 1 \
  -type d \( -name desktop -o -name web -o -name webapp -o -name web-ui \) \
  -printf '%P\n' | sort)
if [[ -n "$banned_directories" ]]; then
  echo "ERROR: a second root-level product Web shell is present" >&2
  printf '%s\n' "$banned_directories" >&2
  exit 1
fi

echo "audit-non-web-boundary: ok"
