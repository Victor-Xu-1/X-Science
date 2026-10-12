"""Validate current connector sources and reproduce one deterministic archive."""
import io
import json
import re
import stat
import zipfile

COMPONENT_VERSION = "0.6.11"
MARK_PATH = "icons/x-science-mark.png"
OLD_ICONS = {f"icons/icon-{size}.png" for size in (16, 32, 48, 128)}
MAX_ENTRIES = 64
MAX_BYTES = 2_000_000


def valid_path(path):
    return (bool(path) and not path.startswith("/") and ".." not in path.split("/")
            and "\\" not in path and not any(ord(char) < 32 for char in path))


def archive_entries(raw):
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        if len(archive.infolist()) > MAX_ENTRIES or sum(item.file_size for item in archive.infolist()) > MAX_BYTES:
            raise ValueError("connector archive exceeds the bounded source contract")
        result = {}
        for item in archive.infolist():
            if item.filename in result or not valid_path(item.filename) or stat.S_ISLNK(item.external_attr >> 16):
                raise ValueError("invalid connector source entry")
            result[item.filename] = archive.read(item)
        return result


def source_entries(directory):
    if directory.is_symlink() or directory.resolve() != directory:
        raise ValueError("connector source root must be a real directory")
    entries = {}
    total = 0
    for path in sorted(directory.rglob("*")):
        if path.is_symlink():
            raise ValueError("connector source must not contain links")
        if path.is_dir():
            continue
        relative = path.relative_to(directory).as_posix()
        if not path.is_file() or not valid_path(relative):
            raise ValueError("invalid connector source entry")
        if relative.endswith((".test.ts", ".test.mjs")):
            continue
        if len(entries) >= MAX_ENTRIES or path.stat().st_size > MAX_BYTES - total:
            raise ValueError("connector source exceeds its bounded contract")
        raw = path.read_bytes()
        total += len(raw)
        entries[relative] = raw
        if len(entries) > MAX_ENTRIES or total > MAX_BYTES:
            raise ValueError("connector source exceeds its bounded contract")
    return entries


def verify_runtime_contract(original, adapted, product_name, mark):
    old = json.loads(original["manifest.json"])
    new = json.loads(adapted["manifest.json"])
    if old["version"] != "0.6.10" or new["version"] != COMPONENT_VERSION:
        raise ValueError("unexpected connector component version")
    if new["name"] != product_name + " Browser Connector":
        raise ValueError("connector does not use the current product identity")
    for manifest in (old, new):
        for key in ("name", "description", "homepage_url", "icons", "version"):
            del manifest[key]
        for key in ("default_title", "default_icon"):
            del manifest["action"][key]
    if old != new:
        raise ValueError("connector namespace migration changed permission, origin or extension identity authority")
    if OLD_ICONS.intersection(adapted) or adapted.get(MARK_PATH) != mark:
        raise ValueError("connector icon is not the approved unmodified artwork")
    for path, raw in adapted.items():
        if path.endswith((".js", ".ts", ".html", ".css", ".json")):
            text = raw.decode("utf-8")
            if path == "local-files-database.js":
                text = text.replace("const PREVIOUS_DATABASE = 'synon-link-local-files'", "")
            if re.search(r"synon(?!ym)", text, re.IGNORECASE):
                raise ValueError("connector contains a current legacy namespace: " + path)


def deterministic_archive(entries):
    if len(entries) > MAX_ENTRIES or sum(map(len, entries.values())) > MAX_BYTES:
        raise ValueError("connector source exceeds its bounded contract")
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(entries):
            if not valid_path(path):
                raise ValueError("invalid connector archive path")
            info = zipfile.ZipInfo(path, (1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = (0o40755 if path.endswith("/") else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, entries[path], compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
    return output.getvalue()
