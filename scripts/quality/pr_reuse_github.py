"""Read and authenticate GitHub Actions PR receipts; never authorize a merge.

The caller must validate the content and equivalence of returned evidence.
All ambiguity and incomplete API data fail closed. No token is logged.
"""

from __future__ import annotations

import hashlib
import io
import ipaddress
import json
import re
import stat
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urljoin, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener
import zipfile


_SHA = re.compile(r"[0-9a-f]{40}")
_REPO = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
_DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
_APP = 15368  # GitHub Actions, checked again on each check-run.
_FILES = frozenset({"scope.json", "plan.json", "summary.json", "events.jsonl", "provenance.json"})
_MAX_JSON = 8 * 1024 * 1024
_MAX_ARCHIVE = 32 * 1024 * 1024
_MAX_MEMBER = 64 * 1024 * 1024
_MAX_EXTRACTED = 96 * 1024 * 1024
_MAX_PAGES = 10


class _EvidenceUnavailable(RuntimeError):
    pass


def _fail(reason: str) -> None:
    # Reasons are fixed literals only; never include response bodies or URLs.
    raise _EvidenceUnavailable("PR evidence unavailable: " + reason)


def _valid_id(value: object) -> bool:
    return type(value) is int and value > 0


def _valid_sha(value: object) -> bool:
    return isinstance(value, str) and _SHA.fullmatch(value) is not None


def _redirect_allowed(url: str) -> bool:
    parsed = urlsplit(url)
    host = (parsed.hostname or "").lower()
    if parsed.scheme != "https" or not host or parsed.username or parsed.password:
        return False
    try:
        if not ipaddress.ip_address(host).is_global:
            return False
    except ValueError:
        if host == "localhost" or host.endswith((".localhost", ".local", ".internal")):
            return False
    # GitHub Actions artifact archives are served from these GitHub/Azure domains.
    return host.endswith((".blob.core.windows.net", ".githubusercontent.com", ".github.com"))


class _SafeRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if not _redirect_allowed(newurl):
            _fail("artifact redirect is not a trusted HTTPS endpoint")
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and urlsplit(req.full_url).hostname != urlsplit(newurl).hostname:
            redirected.remove_header("Authorization")
            redirected.remove_header("authorization")
        return redirected


class _GitHubTransport:
    """Small bounded read-only transport; fake transports may be injected in tests."""

    def __init__(self, api_base: str, token: str, deadline: float, clock):
        parsed = urlsplit(api_base)
        if (parsed.scheme != "https" or parsed.hostname != "api.github.com" or
                parsed.path not in ("", "/") or parsed.query or parsed.fragment or
                parsed.username or parsed.password or not isinstance(token, str) or not token):
            _fail("GitHub API configuration is invalid")
        self._base = api_base.rstrip("/") + "/"
        self._token = token
        self._deadline = deadline
        self._clock = clock
        self._opener = build_opener(_SafeRedirect())

    def _read(self, path: str, limit: int) -> bytes:
        if not isinstance(path, str) or path.startswith("/") or ".." in path.split("/"):
            _fail("GitHub API path is invalid")
        request = Request(urljoin(self._base, path), headers={
            "Accept": "application/vnd.github+json", "Authorization": "Bearer " + self._token,
            "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "x-science-main-evidence",
        })
        remaining = self._deadline - self._clock()
        if remaining <= 0:
            _fail("total evidence deadline elapsed")
        try:
            with self._opener.open(request, timeout=min(20, remaining)) as response:
                data = response.read(limit + 1)
        except (HTTPError, URLError, OSError, TimeoutError, ValueError):
            _fail("GitHub API request failed")
        if len(data) > limit:
            _fail("GitHub API response exceeded its limit")
        if self._clock() >= self._deadline:
            _fail("total evidence deadline elapsed")
        return data

    def get_json(self, path: str):
        try:
            return json.loads(self._read(path, _MAX_JSON))
        except (UnicodeDecodeError, json.JSONDecodeError, RecursionError):
            _fail("GitHub API JSON is invalid")

    def get_archive(self, repository: str, artifact_id: int) -> bytes:
        return self._read(f"repos/{repository}/actions/artifacts/{artifact_id}/zip", _MAX_ARCHIVE)


def _page_items(transport, path: str, field: str | None = None) -> list:
    items = []
    for page in range(1, _MAX_PAGES + 1):
        separator = "&" if "?" in path else "?"
        value = transport.get_json(f"{path}{separator}per_page=100&page={page}")
        if field is None:
            page_items, total = value, None
        elif isinstance(value, dict):
            page_items, total = value.get(field), value.get("total_count")
        else:
            _fail("GitHub API page is invalid")
        if not isinstance(page_items, list) or len(page_items) > 100:
            _fail("GitHub API page is invalid")
        if field is not None and (type(total) is not int or total < 0 or total > 100 * _MAX_PAGES):
            _fail("GitHub API total is invalid or truncated")
        items.extend(page_items)
        if len(page_items) < 100:
            if total is not None and len(items) != total:
                _fail("GitHub API pagination is incomplete")
            return items
    _fail("GitHub API pagination limit reached")


def _one(items: list, predicate, reason: str):
    matches = [item for item in items if isinstance(item, dict) and predicate(item)]
    if len(matches) != 1:
        _fail(reason)
    return matches[0]


def _check_from_run(checks: list, name: str, head: str, run_id: int, repository: str):
    def matches(item):
        details = urlsplit(item.get("details_url") or "")
        return (item.get("name") == name and item.get("head_sha") == head and
                item.get("status") == "completed" and item.get("conclusion") == "success" and
                isinstance(item.get("app"), dict) and item["app"].get("id") == _APP and
                details.scheme == "https" and details.hostname == "github.com" and
                details.path.lower() == ("/" + repository + "/actions/runs/" + str(run_id) +
                                          "/job/" + str(item.get("id"))).lower())
    result = _one(checks, matches, "required trusted check is missing or ambiguous")
    if not _valid_id(result.get("id")):
        _fail("required trusted check identity is invalid")
    return result


def _job(jobs: list, name: str, attempt: int):
    job = _one(jobs, lambda item: item.get("name") == name,
               "required Actions job is missing or ambiguous")
    if (not _valid_id(job.get("id")) or not _valid_id(job.get("run_attempt")) or
            job.get("run_attempt") != attempt or job.get("status") != "completed" or
            job.get("conclusion") != "success"):
        _fail("required Actions job did not succeed")
    return job


def _step(job: dict, name: str) -> None:
    steps = job.get("steps")
    if not isinstance(steps, list):
        _fail("Actions job steps are unavailable")
    step = _one(steps, lambda item: item.get("name") == name,
                "required Actions step is missing or ambiguous")
    if step.get("status") != "completed" or step.get("conclusion") != "success":
        _fail("required Actions step did not succeed")


def _archive_files(data: bytes) -> dict[str, bytes]:
    if not isinstance(data, bytes) or len(data) > _MAX_ARCHIVE:
        _fail("artifact archive is invalid or oversized")
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            entries = archive.infolist()
            if len(entries) > 16 or sum(info.file_size for info in entries) > _MAX_EXTRACTED:
                _fail("artifact archive has too many or too large entries")
            files, seen = {}, set()
            for info in entries:
                mode = info.external_attr >> 16
                if (info.is_dir() or info.filename not in _FILES | {"runtime-phases.jsonl"} or
                        info.filename in seen or info.file_size > _MAX_MEMBER or
                        (mode and stat.S_IFMT(mode) not in (0, stat.S_IFREG))):
                    _fail("artifact archive contains invalid entries")
                seen.add(info.filename)
                if info.filename in _FILES:
                    files[info.filename] = archive.read(info)
            if set(files) != _FILES:
                _fail("artifact lacks required producer evidence")
            return files
    except (zipfile.BadZipFile, OSError, EOFError, RuntimeError) as error:
        if isinstance(error, RuntimeError) and str(error).startswith("PR evidence unavailable:"):
            raise
        _fail("artifact archive could not be read")


class _DeadlineTransport:
    def __init__(self, inner, deadline: float, clock):
        self.inner, self.deadline, self.clock = inner, deadline, clock

    def _check(self):
        if self.clock() >= self.deadline:
            _fail("total evidence deadline elapsed")

    def get_json(self, path: str):
        self._check()
        value = self.inner.get_json(path)
        self._check()
        return value

    def get_archive(self, repository: str, artifact_id: int) -> bytes:
        self._check()
        value = self.inner.get_archive(repository, artifact_id)
        self._check()
        return value


def _fetch_merged_pr_receipts(repository: str, main_sha: str, base_sha: str, token: str,
                              api_base: str, transport, deadline: float, clock) -> dict:
    if (not isinstance(repository, str) or _REPO.fullmatch(repository) is None or
            not _valid_sha(main_sha) or not _valid_sha(base_sha) or main_sha == base_sha):
        _fail("repository or merge identity is invalid")
    inner = transport if transport is not None else _GitHubTransport(api_base, token, deadline, clock)
    transport = _DeadlineTransport(inner, deadline, clock)
    root = "repos/" + repository
    repo = transport.get_json(root)
    if (not isinstance(repo, dict) or not isinstance(repo.get("full_name"), str) or
            repo["full_name"].lower() != repository.lower() or
            not _valid_id(repo.get("id"))):
        _fail("repository identity does not match")
    repo_id = repo["id"]
    associated = _page_items(transport, f"{root}/commits/{main_sha}/pulls")
    if len(associated) != 1 or not isinstance(associated[0], dict) or not _valid_id(associated[0].get("number")):
        _fail("merge does not have one associated PR")
    number = associated[0]["number"]
    pr = transport.get_json(f"{root}/pulls/{number}")
    if not isinstance(pr, dict) or pr.get("number") != number or pr.get("merged") is not True or pr.get("merge_commit_sha") != main_sha:
        _fail("associated PR is not the actual merged PR")
    base, head = pr.get("base"), pr.get("head")
    if (not isinstance(base, dict) or not isinstance(head, dict) or base.get("sha") != base_sha or
            base.get("ref") != "main" or not _valid_sha(head.get("sha")) or
            not isinstance(base.get("repo"), dict) or base["repo"].get("id") != repo_id or
            not isinstance(head.get("repo"), dict) or head["repo"].get("id") != repo_id):
        _fail("PR base, head or repository is not eligible")
    head_sha = head["sha"]
    workflow = transport.get_json(f"{root}/actions/workflows/quality-pr.yml")
    if (not isinstance(workflow, dict) or not _valid_id(workflow.get("id")) or
            workflow.get("path") != ".github/workflows/quality-pr.yml" or workflow.get("state") != "active"):
        _fail("trusted PR workflow is unavailable")
    workflow_id = workflow["id"]
    runs = _page_items(transport, f"{root}/actions/workflows/{workflow_id}/runs?event=pull_request&head_sha={head_sha}", "workflow_runs")
    def matches_identity(item):
        prs = item.get("pull_requests")
        return (item.get("workflow_id") == workflow_id and item.get("event") == "pull_request" and
                item.get("head_sha") == head_sha and _valid_id(item.get("id")) and
                _valid_id(item.get("run_attempt")) and isinstance(item.get("repository"), dict) and
                item["repository"].get("id") == repo_id and isinstance(item.get("head_repository"), dict) and
                item["head_repository"].get("id") == repo_id and isinstance(prs, list) and
                (not prs or len(prs) == 1 and isinstance(prs[0], dict) and prs[0].get("number") == number))
    # The endpoint is already filtered to this workflow/event/head. Do not
    # discard failed, cancelled, pending, or malformed records and then pick an
    # older success: any extra or uncertain record forces ordinary execution.
    if len(runs) != 1 or not isinstance(runs[0], dict) or not matches_identity(runs[0]):
        _fail("original PR workflow run is missing, uncertain or ambiguous")
    run = runs[0]
    if run.get("status") != "completed" or run.get("conclusion") != "success":
        _fail("latest PR workflow attempt did not succeed")
    run_id, attempt = run["id"], run["run_attempt"]
    jobs = _page_items(transport, f"{root}/actions/runs/{run_id}/attempts/{attempt}/jobs", "jobs")
    checks = _page_items(transport, f"{root}/commits/{head_sha}/check-runs?filter=all", "check_runs")
    required_checks = []
    for name in ("Merge Gate / CI required", "PR changed Go packages", "PR policy and static analysis"):
        check, job = _check_from_run(checks, name, head_sha, run_id, repository), _job(jobs, name, attempt)
        if check["id"] != job["id"] or job.get("run_id") != run_id:
            _fail("check and exact-attempt job identities differ")
        required_checks.append(check)
    indices = []
    for job in jobs:
        if isinstance(job, dict):
            match = re.fullmatch(r"PR affected Go partition \(([0-9]+)\)", str(job.get("name", "")))
            if match:
                indices.append(int(match.group(1)))
    if not 1 <= len(indices) <= 4 or sorted(indices) != list(range(len(indices))):
        _fail("PR partition indices are missing, duplicated or unsupported")
    shard_count = len(indices)
    shard_jobs = []
    for index in range(shard_count):
        name = f"PR affected Go partition ({index})"
        check, job = _check_from_run(checks, name, head_sha, run_id, repository), _job(jobs, name, attempt)
        if check["id"] != job["id"] or job.get("run_id") != run_id:
            _fail("shard check and exact-attempt job identities differ")
        for step in ("Test changed Go dependency closure", "Audit PR test source residue", "Upload PR test evidence"):
            _step(job, step)
        required_checks.append(check)
        shard_jobs.append({"id": job["id"], "name": name, "index": index})
    artifacts = _page_items(transport, f"{root}/actions/runs/{run_id}/artifacts", "artifacts")
    if any(isinstance(item, dict) and re.fullmatch(r"pr-runtime-evidence-[0-9]+", str(item.get("name", "")))
           and item["name"] not in {f"pr-runtime-evidence-{i}" for i in range(shard_count)} for item in artifacts):
        _fail("unexpected PR partition artifact")
    output_artifacts = []
    for index in range(shard_count):
        name = f"pr-runtime-evidence-{index}"
        item = _one(artifacts, lambda value: value.get("name") == name,
                    "PR partition artifact is missing or ambiguous")
        origin, digest = item.get("workflow_run"), item.get("digest")
        if (not _valid_id(item.get("id")) or item.get("expired") is not False or
                not isinstance(digest, str) or _DIGEST.fullmatch(digest) is None or
                not isinstance(origin, dict) or origin.get("id") != run_id or
                origin.get("repository_id") != repo_id or origin.get("head_repository_id") != repo_id or
                origin.get("head_sha") != head_sha):
            _fail("artifact provenance is invalid")
        archive = transport.get_archive(repository, item["id"])
        if (not isinstance(archive, bytes) or not _valid_id(item.get("size_in_bytes")) or
                len(archive) != item["size_in_bytes"] or
                "sha256:" + hashlib.sha256(archive).hexdigest() != digest):
            _fail("artifact digest does not match GitHub metadata")
        output_artifacts.append({"id": item["id"], "name": name, "index": index,
                                 "digest": digest, "files": _archive_files(archive)})
    provenances = []
    for item in output_artifacts:
        try:
            value = json.loads(item["files"]["provenance.json"])
        except (UnicodeDecodeError, json.JSONDecodeError, RecursionError):
            _fail("artifact producer provenance is invalid")
        if (not isinstance(value, dict) or value.get("schema") != "x-science.pr-go-execution-provenance.v1" or
                any(type(value.get(key)) is not int for key in
                    ("repository_id", "pr_number", "run_id", "run_attempt", "index", "count")) or
                value.get("repository_id") != repo_id or value.get("pr_number") != number or
                value.get("run_id") != run_id or value.get("run_attempt") != attempt or
                value.get("job") != "pr-go-shards" or value.get("index") != item["index"] or
                value.get("count") != shard_count or value.get("base") != base_sha or value.get("pr_head") != head_sha or
                not _valid_sha(value.get("execution_sha")) or not _valid_sha(value.get("tree"))):
            _fail("artifact producer identity does not match the PR attempt")
        provenances.append(value)
    execution_sha, execution_tree = provenances[0]["execution_sha"], provenances[0]["tree"]
    if any(p["execution_sha"] != execution_sha or p["tree"] != execution_tree for p in provenances):
        _fail("PR artifacts refer to different execution commits")
    synthetic = transport.get_json(f"{root}/git/commits/{execution_sha}")
    main = transport.get_json(f"{root}/git/commits/{main_sha}")
    def parents(commit):
        value = commit.get("parents") if isinstance(commit, dict) else None
        if not isinstance(value, list) or any(not isinstance(p, dict) or not _valid_sha(p.get("sha")) for p in value):
            _fail("Git commit ancestry is unavailable")
        return [p["sha"] for p in value]
    if (not isinstance(synthetic, dict) or synthetic.get("sha") != execution_sha or
            not isinstance(synthetic.get("tree"), dict) or synthetic["tree"].get("sha") != execution_tree or
            parents(synthetic) != [base_sha, head_sha] or not isinstance(main, dict) or
            main.get("sha") != main_sha or not isinstance(main.get("tree"), dict) or
            main["tree"].get("sha") != execution_tree or parents(main) not in ([base_sha], [base_sha, head_sha])):
        _fail("synthetic execution and main tree or ancestry differ")
    return {"repository": {"full_name": repo["full_name"], "id": repo_id},
            "pr": {"number": number, "base_sha": base_sha, "head_sha": head_sha,
                   "merge_commit_sha": main_sha},
            "run": {"id": run_id, "attempt": attempt, "workflow_id": workflow_id, "head_sha": head_sha,
                    "execution_sha": execution_sha, "execution_tree": execution_tree,
                    "execution_parents": [base_sha, head_sha]},
            "checks": [{"id": c["id"], "name": c["name"], "app_id": _APP, "head_sha": head_sha}
                       for c in required_checks],
            "jobs": shard_jobs, "artifacts": output_artifacts}


def fetch_merged_pr_receipts(repository: str, main_sha: str, base_sha: str, token: str,
                             api_base: str = "https://api.github.com", *, transport=None,
                             deadline: float | None = None, clock=time.monotonic) -> dict:
    """Fetch verified PR receipts; any uncertainty raises RuntimeError for fallback.

    Fake transports expose get_json(path) and get_archive(repository, artifact_id).
    The caller separately checks test inventory, results and relevant inputs.
    """
    try:
        deadline = clock() + 25 if deadline is None else deadline
        return _fetch_merged_pr_receipts(repository, main_sha, base_sha, token, api_base, transport, deadline, clock)
    except _EvidenceUnavailable:
        raise
    except Exception:
        _fail("unexpected GitHub response or transport failure")
