"""Generate the adapted connector from immutable Git source; no browser actions."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess

from scripts.packaging.browser_extension_branding import adapt_entries, archive_entries, deterministic_archive

SOURCE_REVISION = "53a8cde66a917eb0b94e0a4f23287eb93328c0fd"
SOURCE_PATH = "assets/synon-link/synon-link-extension-v0.6.10.zip"
SOURCE_SHA256 = "2cb251ad8aedf940f6d3a11b41b74ed55f9b912ab48257a21789b78a1ca0c90b"
MARK_SHA256 = "627c51b57fe4f46f91a3d1effcd87d2698c006da82e8770ffee6404edaeb50fa"
OUTPUT_PATH = "assets/synon-link/x-science-browser-extension-v0.6.10.zip"
PUBLIC_ORIGIN = "https://github.com/Victor-Xu-1/X-Science.git"


def pinned_source(repo, *, fetch_source=False):
    show = ["git", "-C", str(repo), "show", f"{SOURCE_REVISION}:{SOURCE_PATH}"]
    try:
        raw = subprocess.check_output(show, timeout=15)
    except subprocess.CalledProcessError as error:
        if not fetch_source:
            raise ValueError("pinned connector source is unavailable; explicit --fetch-source is required") from error
        present = subprocess.run(["git", "-C", str(repo), "cat-file", "-e", f"{SOURCE_REVISION}^{{commit}}"],
                                 capture_output=True, timeout=15)
        if present.returncode == 0:
            raise ValueError("pinned source commit exists but its package cannot be read") from error
        origin = subprocess.check_output(["git", "-C", str(repo), "remote", "get-url", "origin"], timeout=15).decode().strip()
        if origin != PUBLIC_ORIGIN:
            raise ValueError("pinned source fetch requires the exact public X-Science origin")
        subprocess.check_output(["git", "-C", str(repo), "fetch", "--no-tags", "--depth=1",
                                 "--no-write-fetch-head", "origin", SOURCE_REVISION], timeout=60)
        raw = subprocess.check_output(show, timeout=15)
    if hashlib.sha256(raw).hexdigest() != SOURCE_SHA256:
        raise ValueError("connector source digest mismatch")
    return raw


def build(repo, *, fetch_source=False):
    identity = json.loads((repo / "product-identity.json").read_bytes())
    mark = (repo / "frontend/public/branding/x-science-mark.png").read_bytes()
    return build_archive(pinned_source(repo, fetch_source=fetch_source), identity, mark)


def build_archive(source, identity, mark):
    if hashlib.sha256(source).hexdigest() != SOURCE_SHA256:
        raise ValueError("connector source digest mismatch")
    if hashlib.sha256(mark).hexdigest() != MARK_SHA256:
        raise ValueError("connector mark is not the approved unmodified artwork")
    entries = adapt_entries(archive_entries(source), identity["display_name"], mark)
    return deterministic_archive(entries)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--rollback-source", type=Path)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--fetch-source", action="store_true", help="Fetch only the missing pinned source commit from the exact public origin")
    args = parser.parse_args()
    repo = args.repo.resolve(strict=True)
    raw = build(repo, fetch_source=args.fetch_source)
    output = repo / OUTPUT_PATH
    if output.is_symlink() or output.parent.resolve() != output.parent:
        raise ValueError("connector output must remain in the repository asset directory")
    if args.check:
        if not output.is_file() or output.read_bytes() != raw:
            raise ValueError("adapted connector is not the deterministic reviewed source")
    else:
        if args.rollback_source is None or args.rollback_source.resolve().is_relative_to(repo):
            raise ValueError("preserve the original connector outside source before generating")
        original = pinned_source(repo)
        backup = args.rollback_source
        backup.parent.mkdir(parents=True, exist_ok=True)
        if backup.exists():
            if backup.is_symlink() or backup.read_bytes() != original:
                raise ValueError("rollback source already exists with different ownership or bytes")
        else:
            with backup.open("xb") as handle:
                handle.write(original)
        if output.exists():
            if not output.is_file() or output.read_bytes() != raw:
                raise ValueError("existing adapted connector differs; do not silently overwrite")
        else:
            with output.open("xb") as handle:
                handle.write(raw)
    print(json.dumps({"path": OUTPUT_PATH, "sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw),
                      "upstream_revision": SOURCE_REVISION, "upstream_sha256": SOURCE_SHA256, "checked": args.check}))


if __name__ == "__main__":
    main()
