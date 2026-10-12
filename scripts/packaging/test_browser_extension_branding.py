"""Direct connector adaptation contracts, without installing the extension."""

import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from scripts.packaging.browser_extension_branding import MARK_PATH, OLD_ICONS, LEGACY_LOCATION, adapt_entries, archive_entries
from scripts.packaging.build_browser_extension import build, build_archive, pinned_source, OUTPUT_PATH, SOURCE_REVISION, SOURCE_PATH


class BrowserExtensionBrandingTests(unittest.TestCase):
    repo = Path(__file__).resolve().parents[2]

    def test_direct_cli_check_works_outside_repository(self):
        with tempfile.TemporaryDirectory(prefix="connector-cli-") as directory:
            result = subprocess.run([sys.executable, "-B", str(self.repo / "scripts/packaging/build_browser_extension.py"),
                                     "--repo", str(self.repo), "--check"], cwd=directory,
                                    capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = json.loads(result.stdout)
        self.assertTrue(receipt["checked"])
        self.assertEqual(receipt["path"], OUTPUT_PATH)
        self.assertEqual(receipt["bytes"], (self.repo / OUTPUT_PATH).stat().st_size)

    def test_pinned_source_fetch_is_explicit_bounded_and_identity_checked(self):
        original = pinned_source(self.repo)
        show = ["git", "-C", str(self.repo), "show", f"{SOURCE_REVISION}:{SOURCE_PATH}"]
        remote = ["git", "-C", str(self.repo), "remote", "get-url", "origin"]
        fetch = ["git", "-C", str(self.repo), "fetch", "--no-tags", "--depth=1",
                 "--no-write-fetch-head", "origin", SOURCE_REVISION]
        missing = subprocess.CalledProcessError(128, show, stderr=b"missing pinned object")
        for flag in (False, True):
            with self.subTest(existing_object_flag=flag), mock.patch("subprocess.check_output", return_value=original) as command:
                self.assertEqual(pinned_source(self.repo, fetch_source=flag), original)
                command.assert_called_once_with(show, timeout=15)
        with mock.patch("subprocess.check_output", side_effect=missing) as command:
            with self.assertRaisesRegex(ValueError, "fetch-source"):
                pinned_source(self.repo)
            self.assertEqual(command.call_count, 1)
        for name, origin, restored, commit_present in (
            ("allowed", b"https://github.com/Victor-Xu-1/X-Science.git\n", original, False),
            ("allowed_without_git", b"https://github.com/Victor-Xu-1/X-Science\n", original, False),
            ("wrong_origin", b"https://example.invalid/other.git\n", original, False),
            ("credentials", b"https://user@github.com/Victor-Xu-1/X-Science.git\n", original, False),
            ("other_repository", b"https://github.com/Victor-Xu-1/Other.git\n", original, False),
            ("other_scheme", b"http://github.com/Victor-Xu-1/X-Science.git\n", original, False),
            ("other_host", b"https://github.com.example.invalid/Victor-Xu-1/X-Science.git\n", original, False),
            ("bad_digest", b"https://github.com/Victor-Xu-1/X-Science.git\n", original + b"changed", False),
            ("present_commit_bad_blob", b"https://github.com/Victor-Xu-1/X-Science.git\n", original, True),
        ):
            with self.subTest(case=name), mock.patch("subprocess.check_output", side_effect=[missing, origin, b"", restored]) as command, \
                    mock.patch("subprocess.run", return_value=subprocess.CompletedProcess([], 0 if commit_present else 1)):
                if name in ("allowed", "allowed_without_git"):
                    self.assertEqual(pinned_source(self.repo, fetch_source=True), original)
                    self.assertEqual([call.args[0] for call in command.call_args_list], [show, remote, fetch, show])
                else:
                    with self.assertRaises(ValueError):
                        pinned_source(self.repo, fetch_source=True)
                    fetch_calls = [call.args[0] for call in command.call_args_list if "fetch" in call.args[0]]
                    self.assertEqual(fetch_calls, [fetch] if name == "bad_digest" else [])

    def test_actual_shipped_connector_has_current_brand_and_approved_mark(self):
        self.assertTrue((self.repo / OUTPUT_PATH).is_file(), "current branded connector package is missing")
        entries = archive_entries((self.repo / OUTPUT_PATH).read_bytes())
        expected = archive_entries(build(self.repo))
        self.assertEqual(entries, expected)
        self.assertNotIn("Synon Link", json.loads(entries["manifest.json"])["name"])
        self.assertFalse(OLD_ICONS.intersection(entries))
        residuals = [line.strip() for path, raw in entries.items() if path.endswith((".js", ".html"))
                     for line in raw.decode().splitlines()
                     if re.search(r"(?<![A-Za-z0-9_])Synon(?![A-Za-z0-9_])", line)]
        self.assertCountEqual(residuals, ["return title === DEFAULT_GROUP_TITLE || title === 'Synon'", LEGACY_LOCATION])

    def test_counter_does_not_change_archive_and_wire_authority_is_retained(self):
        original = archive_entries(pinned_source(self.repo))
        mark = (self.repo / "frontend/public/branding/x-science-mark.png").read_bytes()
        first = adapt_entries(original, "X-Science", mark)
        source = pinned_source(self.repo)
        self.assertEqual(build_archive(source, {"display_name": "X-Science", "version": "0.1.99"}, mark),
                         build_archive(source, {"display_name": "X-Science", "version": "1.0.0"}, mark))
        self.assertEqual(first[MARK_PATH], mark)
        self.assertEqual(first["content-script.js"], original["content-script.js"])
        self.assertEqual(json.loads(first["manifest.json"])["version"], "0.6.10")
        with self.assertRaisesRegex(ValueError, "source digest"):
            build_archive(source + b"changed", {"display_name": "X-Science"}, mark)
        with self.assertRaisesRegex(ValueError, "approved unmodified"):
            build_archive(source, {"display_name": "X-Science"}, mark + b"changed")
        for path, raw in first.items():
            if path.endswith(".js"):
                with tempfile.TemporaryDirectory(prefix="connector-static-") as directory:
                    target = Path(directory) / "source.mjs"
                    target.write_bytes(raw)
                    subprocess.run(["node", "--check", str(target)], check=True, capture_output=True)

    def test_old_and_current_tab_group_names_share_existing_policy(self):
        entries = archive_entries(build(self.repo))
        script = entries["background.js"].decode()
        start = script.index("function isSynonTabGroup(group) {")
        end = script.index("\n}", start) + 2
        body = script[start:end]
        checks = ("const DEFAULT_GROUP_TITLE = 'X-Science';\n" + body +
                  "\nif (!isSynonTabGroup({title:'Synon'}) || !isSynonTabGroup({title:'X-Science'}) || "
                  "isSynonTabGroup({title:'other'})) throw Error('group compatibility changed');")
        subprocess.run(["node", "-e", checks], check=True, capture_output=True)


if __name__ == "__main__":
    unittest.main()
