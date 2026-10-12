"""SHA-bound Go namespace proof; literals/directives are never text-normalized."""
from __future__ import annotations

import json
from pathlib import Path
import re
import subprocess
import tempfile

if __package__:
    from . import runtime_input_scope, verification_scope
else:
    import runtime_input_scope
    import verification_scope

OLD_MODULE = "synon-go"
NEW_MODULE = "x-science"
REPLACEMENTS = (
    ("assets/synonbiomed", "assets/x-science"), ("skills/synonbiomed", "skills/x-science"),
    ("cmd/synon", "cmd/x-science"), ("scripts/dev/synon", "scripts/dev/x-science"), ("synon-biomed", "x-science"), ("synon-ai", "x-science"),
    ("synon-go", "x-science"), ("SynonBiomed", "XScience"), ("synonBiomed", "xScience"),
    ("synonbiomed", "xscience"), ("SynonAI", "XScience"), ("SynonAi", "XScience"),
    ("synon_biomed", "x_science"), ("synon_host", "x_science_host"), ("SYNON_", "X_SCIENCE_"),
    ("synon_", "x_science_"), ("synon-", "x-science-"), ("Synon", "XScience"), ("synon", "xscience"),
)


def canonical_path(path: str) -> str:
    for old, new in REPLACEMENTS:
        if old in {"cmd/synon", "scripts/dev/synon"}:
            if path == old or path.startswith(old + "/"):
                path = new + path[len(old):]
            continue
        path = re.sub(old + r"(?!ym)", new, path) if old in {"Synon", "synon"} else path.replace(old, new)
    return path


def git(repo: Path, *arguments: str) -> bytes:
    result = subprocess.run(["git", *arguments], cwd=repo, capture_output=True, check=False)
    if result.returncode:
        raise RuntimeError("namespace proof cannot resolve an exact Git input")
    return result.stdout


def tree(repo: Path, revision: str) -> dict[str, tuple[str, str]]:
    entries = {}
    for row in git(repo, "ls-tree", "-r", "-z", revision).split(b"\0"):
        if not row:
            continue
        metadata, raw_path = row.split(b"\t", 1)
        mode, kind, blob = metadata.decode().split()
        if kind == "blob":
            entries[raw_path.decode()] = (mode, blob)
    return entries


def source_proofs(pairs: list[tuple[str, str]]) -> list[dict]:
    if not pairs:
        return []
    helper = Path(__file__).with_name("go_namespace_equivalence.go")
    with tempfile.TemporaryDirectory(prefix="x-science-go-namespace-proof-") as directory:
        binary = Path(directory) / "namespace-proof"
        build = subprocess.run(["go", "build", "-buildvcs=false", "-o", str(binary), str(helper)],
                               capture_output=True, text=True, check=False)
        if build.returncode:
            raise RuntimeError("namespace AST proof helper did not build")
        lines = "".join(json.dumps({"before": before, "after": after}) + "\n" for before, after in pairs)
        result = subprocess.run([str(binary)], input=lines, capture_output=True, text=True, check=False)
        if result.returncode:
            raise RuntimeError("namespace AST proof did not complete")
        values = [json.loads(line) for line in result.stdout.splitlines()]
        if len(values) != len(pairs) or any(set(item) != {"equivalent", "same_package"} or
                any(type(value) is not bool for value in item.values()) for item in values):
            raise RuntimeError("invalid namespace AST proof result")
        return values


def equivalent_sources(pairs: list[tuple[str, str]]) -> list[bool]:
    return [value["equivalent"] for value in source_proofs(pairs)]


def owned_input_counterparts(repo: Path, old_tree: dict, new_tree: dict) -> dict[str, str]:
    """Route retired inputs to declared current consumers; never omit behavior.

    Ordinary names require a unique actual Git counterpart. Content-addressed
    Conda paths additionally require the same canonical runtime name, platform,
    and exact locked package records from the two immutable catalog inputs.
    """
    owned = set(runtime_input_scope.load(repo))
    owned.update(verification_scope.package_owners(verification_scope.load(repo)))
    result, targets = {}, {}

    def retain(original, current):
        if original == current or original not in old_tree or original in new_tree or current not in new_tree or current not in owned:
            return
        if old_tree[original][0] != new_tree[current][0] or new_tree[current][0] not in {"100644", "100755"}:
            return
        if current in targets and targets[current] != original:
            raise RuntimeError("ambiguous runtime namespace counterpart")
        targets[current], result[original] = original, current

    for original in old_tree:
        if not original.endswith(".go"):
            retain(original, canonical_path(original))
    retain("assets/synon-link/synon-link-extension-v0.6.10.zip",
           "assets/x-science-link/x-science-browser-extension-v0.6.11.zip")
    retain("assets/synon-link/x-science-browser-extension-v0.6.10.zip",
           "assets/x-science-link/x-science-browser-extension-v0.6.11.zip")
    contract = repo / runtime_input_scope.CONTRACT
    groups = json.loads(contract.read_text())["groups"] if contract.is_file() else []
    for group in groups:
        for declaration in group.get("manifests", []):
            catalog = declaration["path"]
            if declaration["kind"] != "conda-catalog" or catalog not in old_tree or catalog not in new_tree:
                continue
            before = json.loads(git(repo, "cat-file", "blob", old_tree[catalog][1]))
            after = json.loads(git(repo, "cat-file", "blob", new_tree[catalog][1]))
            rows = {}
            for row in before.get("runtimes", []):
                key = (canonical_path(row["name"]), row["platform"])
                if key in rows:
                    raise RuntimeError("ambiguous locked runtime namespace counterpart")
                rows[key] = row
            for row in after.get("runtimes", []):
                previous = rows.get((row["name"], row["platform"]))
                if previous is None:
                    continue
                root = declaration["root"] + "/"
                old_manifest, new_manifest = root + previous["manifestPath"], root + row["manifestPath"]
                if old_manifest not in old_tree or new_manifest not in new_tree:
                    continue
                old_lock = json.loads(git(repo, "cat-file", "blob", old_tree[old_manifest][1]))
                new_lock = json.loads(git(repo, "cat-file", "blob", new_tree[new_manifest][1]))
                if (old_lock.get("platform") != new_lock.get("platform")
                        or old_lock.get("packages") is None
                        or old_lock["packages"] != new_lock.get("packages")
                        or canonical_path(old_lock.get("name", "")) != new_lock.get("name")):
                    continue
                for field in ("manifestPath", "explicitPath", "licensesPath"):
                    retain(root + previous[field], root + row[field])
    return result


def filter_paths(repo: Path, base: str, head: str, paths: list[str]) -> list[str]:
    if not all(re.fullmatch(r"(?:[0-9a-f]{40}|[0-9a-f]{64})", value) for value in (base, head)):
        raise ValueError("namespace proof requires immutable commit identities")
    if any(Path(path).is_absolute() or ".." in Path(path).parts for path in paths):
        raise ValueError("namespace proof path is outside source")
    if not any(path.endswith(".go") or canonical_path(path) != path or path.startswith("assets/optional/conda-runtimes/") for path in paths):
        return list(paths)
    # Other repositories and non-canonical checkouts retain their existing scope.
    # The immutable head is checked again below before any proof is accepted.
    current = repo / "go.mod"
    if not current.is_file() or "module x-science\n" not in current.read_text():
        return list(paths)
    old_tree, new_tree = tree(repo, base), tree(repo, head)
    head_module = git(repo, "show", head + ":go.mod").decode()
    if "module x-science\n" not in head_module:
        return list(paths)
    input_counterparts = owned_input_counterparts(repo, old_tree, new_tree)
    paths = list(dict.fromkeys(input_counterparts.get(path, path) for path in paths))
    old_paths = {}
    for path in old_tree:
        if path.endswith(".go"):
            target = canonical_path(path)
            if target in old_paths:
                raise RuntimeError("ambiguous Go namespace counterpart")
            old_paths[target] = path
    requests, candidates = [], []
    for path in paths:
        if not path.endswith(".go"):
            continue
        target = canonical_path(path)
        original = path if path in old_tree else old_paths.get(target)
        if original is None or target not in new_tree:
            continue
        old_mode, old_blob = old_tree[original]
        new_mode, new_blob = new_tree[target]
        if old_mode != new_mode or old_mode not in {"100644", "100755"}:
            continue
        before = git(repo, "cat-file", "blob", old_blob).decode()
        after = git(repo, "cat-file", "blob", new_blob).decode()
        requests.append((before, after))
        candidates.append((path, original, target))
    proved, relocated = set(), {}
    for (path, original, target), proof in zip(candidates, source_proofs(requests)):
        if proof["equivalent"]:
            proved.add(path)
        elif original != target and path not in new_tree and proof["same_package"]:
            # This is ownership routing, not behavior equivalence. Real changed
            # literals/logic stay tested under the exact current package path.
            relocated[path] = target
    return list(dict.fromkeys(relocated.get(path, path) for path in paths if path not in proved))
