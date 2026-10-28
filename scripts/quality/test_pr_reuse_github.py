"""Offline GitHub evidence identity and bounded archive regressions."""
from __future__ import annotations

import copy
import hashlib
import io
import json
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit
from urllib.request import Request
import zipfile

from scripts.quality import pr_reuse_github as reuse


REPOSITORY = "example/science"
REPO_ID = 100
MAIN = "a" * 40
BASE = "b" * 40
HEAD = "c" * 40
EXECUTION = "d" * 40
TREE = "e" * 40
PR = 5
RUN = 900
ATTEMPT = 2
WORKFLOW = 700


class FakeTransport:
    def __init__(self):
        self.repository = {"id": REPO_ID, "full_name": REPOSITORY}
        self.associated = [{"number": PR}]
        self.pr = {"number": PR, "merged": True, "merge_commit_sha": MAIN,
                   "base": {"sha": BASE, "ref": "main", "repo": {"id": REPO_ID}},
                   "head": {"sha": HEAD, "repo": {"id": REPO_ID}}}
        self.workflow = {"id": WORKFLOW, "path": ".github/workflows/quality-pr.yml", "state": "active"}
        self.run = {"id": RUN, "workflow_id": WORKFLOW, "run_attempt": ATTEMPT,
                    "head_sha": HEAD, "event": "pull_request", "status": "completed", "conclusion": "success",
                    "repository": {"id": REPO_ID}, "head_repository": {"id": REPO_ID},
                    "pull_requests": [{"number": PR}]}
        self.extra_runs = []
        self.checks, self.jobs, self.artifacts, self.archives = [], [], [], {}
        self.synthetic = {"sha": EXECUTION, "tree": {"sha": TREE}, "parents": [{"sha": BASE}, {"sha": HEAD}]}
        self.main = {"sha": MAIN, "tree": {"sha": TREE}, "parents": [{"sha": BASE}]}
        names = ["Merge Gate / CI required", "PR changed Go packages", "PR policy and static analysis"]
        names += [f"PR affected Go partition ({i})" for i in range(4)]
        for n, name in enumerate(names):
            jid = 1000 + n
            self.checks.append({"id": jid, "name": name, "head_sha": HEAD, "status": "completed",
                                "conclusion": "success", "app": {"id": 15368},
                                "details_url": f"https://github.com/{REPOSITORY}/actions/runs/{RUN}/job/{jid}"})
            self.jobs.append({"id": jid, "name": name, "run_id": RUN, "run_attempt": ATTEMPT,
                              "status": "completed", "conclusion": "success", "steps": [
                                  {"name": step, "status": "completed", "conclusion": "success"} for step in
                                  ("Test changed Go dependency closure", "Audit PR test source residue", "Upload PR test evidence")]})
        for i in range(4):
            provenance = {"schema": "x-science.pr-go-execution-provenance.v1", "repository_id": REPO_ID,
                          "pr_number": PR, "base": BASE, "pr_head": HEAD, "execution_sha": EXECUTION,
                          "tree": TREE, "run_id": RUN, "run_attempt": ATTEMPT, "job": "pr-go-shards",
                          "index": i, "count": 4}
            self.set_archive(i, self.make_archive(provenance))

    @staticmethod
    def make_archive(provenance, *, extra=None, missing=None):
        data = io.BytesIO()
        with zipfile.ZipFile(data, "w", zipfile.ZIP_DEFLATED) as archive:
            for name in ("scope.json", "plan.json", "summary.json", "events.jsonl", "provenance.json"):
                if name != missing:
                    archive.writestr(name, json.dumps(provenance) if name == "provenance.json" else "{}")
            if extra:
                archive.writestr(extra, "x")
        return data.getvalue()

    def set_archive(self, index, archive):
        artifact_id = 2000 + index
        artifact = {"id": artifact_id, "name": f"pr-runtime-evidence-{index}", "expired": False,
                    "size_in_bytes": len(archive), "digest": "sha256:" + hashlib.sha256(archive).hexdigest(),
                    "workflow_run": {"id": RUN, "repository_id": REPO_ID,
                                     "head_repository_id": REPO_ID, "head_sha": HEAD}}
        self.artifacts = [item for item in self.artifacts if item["id"] != artifact_id] + [artifact]
        self.archives[artifact_id] = archive

    def get_archive(self, repository, artifact_id):
        if repository != REPOSITORY:
            raise ValueError("unexpected repository")
        return self.archives[artifact_id]

    def get_json(self, request):
        path = urlsplit("https://fixture/" + request).path.removeprefix("/")
        root = "repos/" + REPOSITORY
        if path == root:
            return copy.deepcopy(self.repository)
        if path == f"{root}/commits/{MAIN}/pulls":
            return copy.deepcopy(self.associated)
        if path == f"{root}/pulls/{PR}":
            return copy.deepcopy(self.pr)
        if path == f"{root}/actions/workflows/quality-pr.yml":
            return copy.deepcopy(self.workflow)
        if path == f"{root}/actions/workflows/{WORKFLOW}/runs":
            runs = [copy.deepcopy(self.run), *copy.deepcopy(self.extra_runs)]
            return {"total_count": len(runs), "workflow_runs": runs}
        if path == f"{root}/actions/runs/{RUN}/attempts/{ATTEMPT}/jobs":
            return {"total_count": len(self.jobs), "jobs": copy.deepcopy(self.jobs)}
        if path == f"{root}/commits/{HEAD}/check-runs":
            return {"total_count": len(self.checks), "check_runs": copy.deepcopy(self.checks)}
        if path == f"{root}/actions/runs/{RUN}/artifacts":
            return {"total_count": len(self.artifacts), "artifacts": copy.deepcopy(self.artifacts)}
        if path == f"{root}/git/commits/{EXECUTION}":
            return copy.deepcopy(self.synthetic)
        if path == f"{root}/git/commits/{MAIN}":
            return copy.deepcopy(self.main)
        raise ValueError("unexpected fixture request")


class GitHubReceiptTests(unittest.TestCase):
    def test_total_deadline_stops_after_the_first_slow_response(self):
        now, calls = [0.0], []

        class SlowTransport(FakeTransport):
            def get_json(self, request):
                calls.append(request)
                value = super().get_json(request)
                now[0] = 2.0
                return value

        with self.assertRaisesRegex(RuntimeError, "deadline elapsed"):
            reuse.fetch_merged_pr_receipts(REPOSITORY, MAIN, BASE, "token", transport=SlowTransport(),
                                           deadline=1.0, clock=lambda: now[0])
        self.assertEqual(len(calls), 1)

    def setUp(self):
        self.transport = FakeTransport()

    def fetch(self):
        return reuse.fetch_merged_pr_receipts(REPOSITORY, MAIN, BASE, "unused", transport=self.transport)

    def assert_unavailable(self):
        with self.assertRaisesRegex(RuntimeError, "^PR evidence unavailable:"):
            self.fetch()

    def test_verified_receipt_retains_original_execution_identity(self):
        receipt = self.fetch()
        self.assertEqual(receipt["run"]["execution_sha"], EXECUTION)
        self.assertEqual(receipt["run"]["execution_tree"], TREE)
        self.assertEqual(receipt["run"]["execution_parents"], [BASE, HEAD])
        self.assertEqual(receipt["pr"]["head_sha"], HEAD)
        self.assertEqual([a["index"] for a in receipt["artifacts"]], list(range(4)))
        self.assertEqual(set(receipt["artifacts"][0]["files"]), reuse._FILES)

    def test_run_may_omit_pr_association_but_conflicting_associations_fail(self):
        # GitHub can return an empty run.pull_requests even for a real PR run;
        # exact PR, head/base, producer and synthetic parents are still checked.
        self.transport.run["pull_requests"] = []
        self.assertEqual(self.fetch()["pr"]["number"], PR)
        for associations in ([{"number": PR + 1}], [{"number": PR}, {"number": PR + 1}], None):
            with self.subTest(associations=associations):
                self.transport = FakeTransport()
                self.transport.run["pull_requests"] = associations
                self.assert_unavailable()

    def test_one_to_four_exact_partitions_are_supported(self):
        for count in range(1, 5):
            with self.subTest(count=count):
                self.transport = FakeTransport()
                keep = {f"PR affected Go partition ({i})" for i in range(count)}
                self.transport.jobs = [job for job in self.transport.jobs
                                       if not job["name"].startswith("PR affected Go partition (") or job["name"] in keep]
                self.transport.checks = [check for check in self.transport.checks
                                         if not check["name"].startswith("PR affected Go partition (") or check["name"] in keep]
                self.transport.artifacts = [a for a in self.transport.artifacts if a["id"] < 2000 + count]
                for i in range(count):
                    with zipfile.ZipFile(io.BytesIO(self.transport.archives[2000 + i])) as archive:
                        provenance = json.loads(archive.read("provenance.json"))
                    self.transport.set_archive(i, self.transport.make_archive({**provenance, "count": count}))
                self.assertEqual(len(self.fetch()["artifacts"]), count)

    def test_repository_fork_pr_and_merge_identity_fail_closed(self):
        mutations = [
            lambda t: t.repository.update(id=999),
            lambda t: t.pr["head"]["repo"].update(id=999),
            lambda t: t.pr["base"].update(sha="f" * 40),
            lambda t: t.pr.update(merged=False),
            lambda t: t.pr.update(merge_commit_sha="f" * 40),
            lambda t: t.associated.append({"number": 6}),
            lambda t: t.run["head_repository"].update(id=999),
            lambda t: t.run.update(head_sha="f" * 40),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                self.transport = FakeTransport()
                mutate(self.transport)
                self.assert_unavailable()

    def test_untrusted_workflow_checks_attempt_and_shards_fail_closed(self):
        mutations = [
            lambda t: t.workflow.update(path=".github/workflows/other.yml"),
            lambda t: t.run.update(workflow_id=999),
            lambda t: t.run.update(conclusion="cancelled"),
            lambda t: t.run.update(run_attempt=1),
            lambda t: t.checks[0]["app"].update(id=999),
            lambda t: t.checks[4].update(conclusion="failure"),
            lambda t: t.jobs[3].update(run_attempt=1),
            lambda t: t.jobs[4].update(conclusion="cancelled"),
            lambda t: t.jobs.pop(),
            lambda t: t.jobs[3]["steps"][2].update(conclusion="skipped"),
            lambda t: t.checks[3].update(details_url=f"https://github.com/other/repo/actions/runs/{RUN}/job/{t.checks[3]['id']}"),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                self.transport = FakeTransport()
                mutate(self.transport)
                self.assert_unavailable()

    def test_success_followed_by_failure_cancellation_or_pending_falls_back(self):
        for status, conclusion in (("completed", "failure"), ("completed", "cancelled"), ("in_progress", None)):
            with self.subTest(status=status, conclusion=conclusion):
                self.transport = FakeTransport()
                newer = copy.deepcopy(self.transport.run)
                newer.update(id=RUN + 1, status=status, conclusion=conclusion)
                self.transport.extra_runs.append(newer)
                self.assert_unavailable()

    def test_multiple_successful_or_unknown_records_also_fall_back(self):
        for state in ("success", "unknown"):
            with self.subTest(state=state):
                self.transport = FakeTransport()
                newer = copy.deepcopy(self.transport.run)
                newer["id"] = RUN + 1
                if state == "unknown":
                    del newer["repository"]
                self.transport.extra_runs.append(newer)
                self.assert_unavailable()

    def test_missing_duplicate_expired_and_digest_mismatch_artifacts_fail_closed(self):
        mutations = [
            lambda t: t.artifacts.pop(),
            lambda t: t.artifacts.append(copy.deepcopy(t.artifacts[0])),
            lambda t: t.artifacts[0].update(expired=True),
            lambda t: t.artifacts[0].update(digest="sha256:" + "0" * 64),
            lambda t: t.artifacts[0]["workflow_run"].update(repository_id=999),
            lambda t: t.archives.update({2000: b"invalid"}),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                self.transport = FakeTransport()
                mutate(self.transport)
                self.assert_unavailable()

    def test_old_provenance_attempt_and_archive_traversal_fail_closed(self):
        original = self.transport
        with zipfile.ZipFile(io.BytesIO(original.archives[2000])) as archive:
            provenance = json.loads(archive.read("provenance.json"))
        for replacement in (
            original.make_archive(provenance, missing="provenance.json"),
            original.make_archive({**provenance, "run_attempt": 1}),
            original.make_archive(provenance, extra="../escape"),
        ):
            self.transport = FakeTransport()
            self.transport.set_archive(0, replacement)
            self.assert_unavailable()

    def test_archive_member_size_limit_fails_closed(self):
        with patch.object(reuse, "_MAX_MEMBER", 2):
            self.assert_unavailable()

    def test_artifact_redirect_does_not_forward_api_token(self):
        request = Request("https://api.github.com/repos/example/science/actions/artifacts/1/zip",
                          headers={"Authorization": "Bearer private-token"})
        redirect = reuse._SafeRedirect().redirect_request(
            request, None, 302, "Found", {}, "https://example.blob.core.windows.net/container/archive")
        self.assertNotIn("Authorization", redirect.headers)
        with self.assertRaises(RuntimeError):
            reuse._SafeRedirect().redirect_request(request, None, 302, "Found", {}, "https://127.0.0.1/archive")

    def test_tree_and_parent_mismatch_fail_closed(self):
        for mutate in (lambda t: t.main["tree"].update(sha="f" * 40),
                       lambda t: t.synthetic["parents"].reverse(),
                       lambda t: t.main["parents"][0].update(sha="f" * 40)):
            with self.subTest(mutation=mutate):
                self.transport = FakeTransport()
                mutate(self.transport)
                self.assert_unavailable()

    def test_truncated_api_pagination_fails_closed(self):
        original = self.transport.get_json
        def incomplete(path):
            value = original(path)
            if "/runs?event=" in path:
                value["total_count"] = 2
            return value
        self.transport.get_json = incomplete
        self.assert_unavailable()

    def test_different_execution_and_boolean_provenance_fields_fail_closed(self):
        for field, replacement in (("execution_sha", "f" * 40), ("index", True)):
            with self.subTest(field=field):
                self.transport = FakeTransport()
                with zipfile.ZipFile(io.BytesIO(self.transport.archives[2000])) as archive:
                    provenance = json.loads(archive.read("provenance.json"))
                provenance[field] = replacement
                self.transport.set_archive(0, self.transport.make_archive(provenance))
                self.assert_unavailable()

    def test_unexpected_transport_error_is_sanitized(self):
        def bad(_path):
            raise ValueError("secret-token-sensitive-url")
        self.transport.get_json = bad
        with self.assertRaises(RuntimeError) as caught:
            self.fetch()
        self.assertNotIn("secret", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
