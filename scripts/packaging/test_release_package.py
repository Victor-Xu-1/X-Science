"""Regression contracts for published-release package inputs."""

import copy
import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

from scripts.packaging import release_package as packaging
from scripts.packaging import oci_bundle


class ReleasePackageTest(unittest.TestCase):
    def test_post_login_tag_check_keeps_the_authoritative_package_slug(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            event = root / 'event.json'
            event.write_text('{}')
            environment = {'GITHUB_EVENT_NAME': 'release', 'GITHUB_EVENT_PATH': str(event),
                           'GITHUB_ACTOR': 'fixture', 'GH_TOKEN': 'synthetic-not-a-token',
                           'GITHUB_STEP_SUMMARY': str(root / 'summary.md')}
            with patch.dict(os.environ, environment), patch.object(
                packaging, 'prepare', return_value=('v0.1.5', 'a' * 40, root),
            ), patch.object(packaging.candidate, 'load_controls', return_value=({'machine_slug': 'x-science'}, {})), \
                    patch.object(packaging, 'build_layout', return_value='sha256:' + 'a' * 64), \
                    patch.object(packaging, 'verify_roundtrip'), patch.object(packaging.subprocess, 'run'), \
                    patch.object(packaging, 'ensure_unused_package_tag', autospec=True) as guard:
                with contextlib.redirect_stdout(io.StringIO()):
                    packaging.main()
            guard.assert_called_once_with('v0.1.5', 'x-science')

    def test_package_tag_check_uses_the_authoritative_slug_and_rejects_reuse(self):
        with patch.object(packaging, 'api_json', return_value=None) as request:
            packaging.ensure_unused_package_tag('v0.1.3', 'x-science')
            request.assert_called_once_with(
                'users/Victor-Xu-1/packages/container/x-science/versions?per_page=100&page=1',
                absent_ok=True,
            )
        version = {'metadata': {'container': {'tags': ['v0.1.3']}}}
        with patch.object(packaging, 'api_json', return_value=[version]):
            with self.assertRaisesRegex(ValueError, 'already exists'):
                packaging.ensure_unused_package_tag('v0.1.3', 'x-science')

    def setUp(self):
        self.identity = {"machine_slug": "synon-biomed", "version": "0.1.1"}
        self.revision = "a" * 40
        self.event = {
            "action": "published",
            "repository": {"full_name": packaging.REPOSITORY, "id": packaging.REPOSITORY_ID},
            "release": {"tag_name": "v0.1.1", "draft": False, "prerelease": False, "immutable": True},
        }

    def test_only_published_immutable_version_is_accepted(self):
        self.assertEqual(packaging.validate_event(self.event, self.identity), "v0.1.1")
        for path, value in [
            (("action",), "edited"),
            (("repository", "id"), 1),
            (("repository", "full_name"), "someone/else"),
            (("release", "draft"), True),
            (("release", "immutable"), False),
            (("release", "tag_name"), "v0.1.2"),
            (("release", "tag_name"), "v0.1.1; touch /tmp/unwanted"),
        ]:
            with self.subTest(path=path, value=value):
                event = copy.deepcopy(self.event)
                target = event
                for key in path[:-1]:
                    target = target[key]
                target[path[-1]] = value
                with self.assertRaises(ValueError):
                    packaging.validate_event(event, self.identity)

    def test_full_quality_must_match_revision_attempt_and_repository(self):
        manifest = {"source_commit": self.revision, "candidate_run_id": 10, "candidate_run_attempt": 2}
        run = {
            "id": 10, "run_attempt": 2, "head_sha": self.revision, "head_branch": "main",
            "status": "completed", "conclusion": "success",
            "path": ".github/workflows/quality.yml",
            "repository": {"id": packaging.REPOSITORY_ID},
        }
        packaging.validate_run(run, manifest)
        for key, value in [
            ("conclusion", "failure"), ("status", "in_progress"), ("head_sha", "b" * 40),
            ("run_attempt", 1), ("head_branch", "task"), ("path", ".github/workflows/quality-pr.yml"),
            ("repository", {"id": 1}), ("id", 11),
        ]:
            with self.subTest(key=key):
                bad = {**run, key: value}
                with self.assertRaises(ValueError):
                    packaging.validate_run(bad, manifest)

    def test_bundle_rejects_directories_and_links(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "directory").mkdir()
            with self.assertRaises(ValueError):
                oci_bundle.files(root)
            (root / "directory").rmdir()
            (root / "link").symlink_to(root / "absent")
            with self.assertRaises(ValueError):
                oci_bundle.files(root)

    @unittest.skipUnless(shutil.which("oras"), "ORAS is required for the real OCI roundtrip")
    def test_real_oci_roundtrip_preserves_exact_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifacts = root / "artifacts"
            artifacts.mkdir()
            (artifacts / "binary.tar.gz").write_bytes(os.urandom(1024))
            (artifacts / "manifest.json").write_text('{"test": true}\n')
            value = oci_bundle.build_layout(artifacts, root / "layout", "v0.1.1", self.revision, packaging.REPOSITORY)
            oci_bundle.verify_roundtrip(artifacts, root / "restored", f"{root / 'layout'}@{value}", local=True)
            (artifacts / "binary.tar.gz").write_bytes(b"changed")
            with self.assertRaisesRegex(ValueError, "changed"):
                oci_bundle.verify_roundtrip(artifacts, root / "tampered", f"{root / 'layout'}@{value}", local=True)


if __name__ == "__main__":
    unittest.main()
