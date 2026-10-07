"""Host-selected output control and integrity-bound resumable transport.

Provider adapters supply exec only. The host owns selection and persistence;
this module never launches or repeats the scientific command.
"""
import hashlib
import os
import re
import shlex
import shutil
import json
import tempfile

from . import ByocError, CHUNK, WORK

_SHA = re.compile(r"^[0-9a-f]{64}$")
_REMOTE = re.compile(r"^(?:inventory\.nul|checkpoint-[0-9a-f]{64}\.jsonl|selection-[0-9a-f]{64}\.nul|archive-[0-9a-f]{64}-[0-9a-f]{64}\.tar\.gz)$")


def _digest(path, progress=None):
    result = hashlib.sha256()
    observed = 0
    with open(path, "rb") as source:
        for data in iter(lambda: source.read(CHUNK), b""):
            result.update(data)
            observed += len(data)
            if progress:
                progress(observed)
    return result.hexdigest()


class _Progress:
    def __init__(self, stage, identity):
        self.stage, self.identity, self.last = stage, identity, {}

    def update(self, phase, count):
        previous = self.last.get(phase)
        if previous is not None and count - previous < 1 << 20:
            return
        self.last[phase] = count
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", dir=self.stage, prefix=".transfer-progress-", delete=False) as target:
                temporary = target.name
                json.dump({"identity": self.identity, "phase": phase, "bytes": count}, target)
            os.replace(temporary, os.path.join(self.stage, ".transfer-progress.json"))
        except OSError:
            # Observability is best effort; it never discards transfer data.
            if temporary:
                try:
                    os.unlink(temporary)
                except OSError:
                    pass


def _regular(path):
    if os.path.lexists(path) and (os.path.islink(path) or not os.path.isfile(path)):
        raise ByocError("result_rejected", "transfer path is not a regular file")


def _control(provider, sid, command, stdin=None):
    process = provider.exec(sid, ["bash", "--noprofile", "--norc", "-p", "-c", command], stdin=stdin)
    output = bytearray()
    for data in process.stdout:
        if len(output) + len(data) > 256:
            raise ByocError("result_rejected", "output control receipt is oversized")
        output.extend(data)
    if process.wait() != 0:
        raise ByocError("transient", "output control operation has no successful receipt")
    return output.decode("ascii", "strict").strip()


def _download(provider, sid, stage, name, sha, size):
    if not _REMOTE.fullmatch(name) or not isinstance(sha, str) or not _SHA.fullmatch(sha) or type(size) is not int or size < 0:
        raise ByocError("invalid_request", "output transfer identity is invalid")
    local_name = "inventory.nul" if name == "inventory.nul" else "out.tar.gz"
    if name.startswith("checkpoint-"):
        local_name = "checkpoint.jsonl"
    target = os.path.join(stage, local_name)
    partial = target + ".partial"
    identity = partial + ".identity"
    progress = _Progress(stage, sha)
    progress.update("admitted", 0)
    for path in (target, partial, identity):
        _regular(path)
    if os.path.isfile(target):
        if os.path.getsize(target) != size or _digest(target, lambda count: progress.update("verify-completed", count)) != sha:
            raise ByocError("result_rejected", "completed output identity changed")
        return {"ok": True, "ready": True, "sha256": sha, "bytes": size}
    expected = f"{sha}:{size}"
    if os.path.isfile(identity):
        with open(identity, encoding="ascii") as source:
            if source.read(128) != expected:
                raise ByocError("result_rejected", "partial output belongs to another receipt")
    else:
        if os.path.exists(partial):
            raise ByocError("result_rejected", "partial output has no identity")
        fd = os.open(identity, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="ascii") as output:
            output.write(expected)
    offset = os.path.getsize(partial) if os.path.exists(partial) else 0
    if offset > size:
        raise ByocError("result_rejected", "partial output exceeds its receipt")
    # This is current physical admission, not an implicit dataset cap.
    if size - offset > max(0, shutil.disk_usage(stage).free - (32 << 20)):
        raise ByocError("quota_exhausted", "output transfer is waiting for local storage capacity")
    remote = shlex.quote(f"{WORK}/.synon-harvest/{name}")
    if offset:
        arguments = ["bash", "--noprofile", "--norc", "-p", f"{WORK}/_synon_harvest.sh", "prefix", name, str(offset), sha]
        observed = _control(provider, sid, " ".join(shlex.quote(value) for value in arguments))
        if observed in {"working", "unknown"}:
            return {"ok": True, "ready": False, "transfer_state": "validating_prefix"}
        fields = observed.split(":")
        if len(fields) != 3 or fields[0] != "ready" or not _SHA.fullmatch(fields[1]) or fields[2] != str(offset):
            raise ByocError("result_rejected", "remote prefix verification has no valid receipt")
        if fields[1] != _digest(partial, lambda count: progress.update("verify-prefix", count)):
            raise ByocError("result_rejected", "remote output prefix changed; partial data retained")
    command = f"set -eu; test -f {remote}; test ! -L {remote}; tail -c +{offset + 1} -- {remote}"
    process = provider.exec(sid, ["bash", "--noprofile", "--norc", "-p", "-c", command])
    fd = os.open(partial, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "ab", buffering=0) as output:
        for data in process.stdout:
            if len(data) > size - offset:
                raise ByocError("result_rejected", "output stream exceeds its immutable receipt")
            if len(data) > max(0, shutil.disk_usage(stage).free - (32 << 20)):
                raise ByocError("quota_exhausted", "output transfer is waiting for local storage capacity; partial data retained")
            written = output.write(data)
            if written != len(data):
                raise ByocError("transient", "partial output write interrupted; data retained")
            offset += len(data)
            progress.update("download", offset)
        os.fsync(output.fileno())
    if process.wait() != 0 or offset != size:
        raise ByocError("transient", "output transfer interrupted; verified prefix retained")
    if _digest(partial, lambda count: progress.update("verify-final", count)) != sha:
        raise ByocError("result_rejected", "output digest mismatch; partial data retained")
    os.replace(partial, target)
    return {"ok": True, "ready": True, "sha256": sha, "bytes": size}


def harvest_operation(provider, request, stage):
    sid, action = request["sandbox_id"], request["harvest"]
    helper = f"{WORK}/_synon_harvest.sh"
    if action == "install":
        source = os.path.join(stage, "_synon_harvest.sh")
        _regular(source)
        with open(source, "rb") as body:
            digest = _digest(source)
            command = (f"set -eu; umask 077; cd {WORK}; test ! -L _synon_harvest.sh; "
                       "next=$(mktemp .synon-helper.XXXXXXXX); trap 'rm -f -- \"$next\"' EXIT; cat >\"$next\"; "
                       f"test \"$(sha256sum -- \"$next\" | cut -d' ' -f1)\" = {digest}; "
                       "chmod 700 -- \"$next\"; mv -fT -- \"$next\" _synon_harvest.sh; printf installed")
            response = _control(provider, sid, command, iter(lambda: body.read(CHUNK), b""))
        return {"ok": response == "installed", "installed": response == "installed"}
    if action == "fetch":
        return _download(provider, sid, stage, request.get("name", ""), request.get("sha256"), request.get("bytes"))
    args = ["inventory"]
    if action == "checkpoint":
        manifest, key = request.get("source_sha256"), request.get("selection_sha256")
        if not isinstance(manifest, str) or not manifest.startswith("out/") or not isinstance(key, str) or not _SHA.fullmatch(key):
            raise ByocError("invalid_request", "checkpoint control identity is invalid")
        command = " ".join(shlex.quote(value) for value in ["bash", "--noprofile", "--norc", "-p", helper, "checkpoint", manifest, key])
        return {"ok": True, "receipt": _control(provider, sid, command)}
    if action in {"selection", "archive"}:
        source_sha, selected_sha = request.get("source_sha256"), request.get("selection_sha256")
        if not isinstance(source_sha, str) or not _SHA.fullmatch(source_sha) or not isinstance(selected_sha, str) or not _SHA.fullmatch(selected_sha):
            raise ByocError("invalid_request", "output selection identity is invalid")
        if action == "selection":
            source = os.path.join(stage, "selection.nul")
            _regular(source)
            if _digest(source) != selected_sha:
                raise ByocError("result_rejected", "host selection digest changed")
            name = f".synon-harvest/selection-{selected_sha}.nul"
            command = (f"set -eu; umask 077; cd {WORK}; test -d .synon-harvest; test ! -L .synon-harvest; "
                       "next=$(mktemp .synon-harvest/selection.XXXXXXXX); trap 'rm -f -- \"$next\"' EXIT; cat >\"$next\"; "
                       f"test \"$(sha256sum -- \"$next\" | cut -d' ' -f1)\" = {selected_sha}; mv -fT -- \"$next\" {name}; printf selected")
            with open(source, "rb") as body:
                response = _control(provider, sid, command, iter(lambda: body.read(CHUNK), b""))
            return {"ok": response == "selected", "selected": response == "selected"}
        args = ["archive", source_sha, selected_sha]
    elif action != "inventory":
        raise ByocError("invalid_request", "unknown output control action")
    command = " ".join(shlex.quote(value) for value in ["bash", "--noprofile", "--norc", "-p", helper, *args])
    return {"ok": True, "receipt": _control(provider, sid, command)}
