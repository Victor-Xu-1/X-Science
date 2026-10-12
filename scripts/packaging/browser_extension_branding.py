"""Adapt only the pinned connector's displayed branding, retaining wire identity."""

import copy
import io
import json
import re
import zipfile

COMPONENT_VERSION = "0.6.10"
MARK_PATH = "icons/x-science-mark.png"
OLD_ICONS = {f"icons/icon-{size}.png" for size in (16, 32, 48, 128)}
GROUP_MATCH = "return String(group?.title || '').trim() === DEFAULT_GROUP_TITLE"
LEGACY_LOCATION = "if (normalized.includes('/Synon/')) return `浏览器下载目录 / Synon / ${basename}`"
DISPLAY_LINES = {
    "workspace.js": (84, 119, 126, 153, 182, 218),
    "popup.js": (103, 111, 117, 121, 158),
    "options.js": (63, 69, 73),
    "approval.js": (39, 42, 53, 56),
    "personalization.js": (19,),
    "link-runtime.js": (110,),
    "background.js": (
        334, 340, 532, 539, 966, 1005, 1013, 1069, 1082, 1083, 1125, 1171, 1264,
        1872, 1953, 1986, 2049, 2083, 2105, 2159, 2194, 2204, 2205, 2220, 2228,
        2229, 2458, 2472, 2492, 2495, 2497, 2499, 2502, 2504, 2562, 2592, 2603,
        2618, 2711, 2715, 2716, 2721, 2789, 2813, 2815, 2820, 2936, 3040, 3055, 3204, 4691,
        4716, 4744, 4865, 5092, 5106, 5146, 5172, 5173, 5174, 5202, 5203, 5254,
        5255, 5286, 5287, 5317, 5318, 5342, 5383, 5489, 5495, 5928, 6003, 6012, 6083,
    ),
    "popup.html": (6, 14, 38),
    "personalization.html": (6, 13, 15, 33),
    "approval.html": (6, 13, 15, 35),
    "options.html": (6, 12, 13),
    "INSTALL.html": (6, 126, 127, 128, 135, 136, 142, 160, 166, 172, 177, 178),
    "workspace.html": (6, 13, 14, 26, 43),
}


def archive_entries(raw):
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        if len(archive.infolist()) > 64 or sum(item.file_size for item in archive.infolist()) > 2_000_000:
            raise ValueError("connector archive exceeds the pinned bounded source contract")
        result = {}
        for item in archive.infolist():
            if (item.filename in result or item.filename.startswith("/") or
                    ".." in item.filename.split("/") or "\\" in item.filename):
                raise ValueError("invalid connector source entry")
            result[item.filename] = archive.read(item)
        return result


def _display_line(text, product_name):
    # Only explicitly inventoried display lines from the digest-pinned source
    # reach this function; no full JavaScript text replacement is performed.
    connector = product_name + " Browser Connector"
    text = re.sub(r"(?<![A-Za-z0-9_])Synon Link(?![A-Za-z0-9_])", lambda _: connector, text)
    return re.sub(r"(?<![A-Za-z0-9_])Synon(?![A-Za-z0-9_])", lambda _: product_name, text)


def adapt_entries(original, product_name, mark):
    if not re.fullmatch(r"[A-Za-z][A-Za-z0-9 -]{0,79}", product_name):
        raise ValueError("product display name must be safe in the pinned literal contexts")
    manifest = json.loads(original["manifest.json"])
    if manifest["version"] != COMPONENT_VERSION or manifest["name"] != "Synon Link":
        raise ValueError("unexpected connector component source")
    adapted = copy.deepcopy(original)
    for path in original:
        if path.endswith((".js", ".html")):
            text = original[path].decode("utf-8")
            lines = text.splitlines(keepends=True)
            for number in DISPLAY_LINES.get(path, ()):
                before = lines[number - 1]
                after = _display_line(before, product_name)
                expected_templates = [value.replace("'Synon'", f"'{product_name}'")
                                      for value in re.findall(r"\$\{[^}]*\}", before)]
                if before == after or expected_templates != re.findall(r"\$\{[^}]*\}", after):
                    raise ValueError(f"pinned display/template contract changed: {path}:{number}")
                lines[number - 1] = after
            text = "".join(lines)
            if path == "background.js":
                if text.count(LEGACY_LOCATION) != 1 or text.count(GROUP_MATCH) != 1:
                    raise ValueError("pinned legacy directory/group contract changed")
                title = json.dumps(product_name, ensure_ascii=False)
                text = text.replace("const DEFAULT_GROUP_TITLE = 'Synon'", f"const DEFAULT_GROUP_TITLE = {title}")
                text = text.replace(GROUP_MATCH, "const title = String(group?.title || '').trim()\n  "
                                    "return title === DEFAULT_GROUP_TITLE || title === 'Synon'")
                current_location = (f"if (normalized.includes('/{product_name}/')) return "
                                    f"`浏览器下载目录 / {product_name} / ${{basename}}`\n  ")
                text = text.replace(LEGACY_LOCATION, current_location + LEGACY_LOCATION)
            for old in OLD_ICONS:
                text = text.replace(old, MARK_PATH)
            adapted[path] = text.encode()
    manifest["name"] = product_name + " Browser Connector"
    manifest["description"] = product_name + " 的浏览器连接、本地文件授权和本地增强能力入口。"
    manifest["homepage_url"] = "https://github.com/Victor-Xu-1/X-Science"
    manifest["icons"] = {key: MARK_PATH for key in manifest["icons"]}
    manifest["action"]["default_title"] = manifest["name"]
    manifest["action"]["default_icon"] = {key: MARK_PATH for key in manifest["action"]["default_icon"]}
    adapted["manifest.json"] = (json.dumps(manifest, indent=2, ensure_ascii=False) + "\n").encode()
    for path in OLD_ICONS:
        del adapted[path]
    adapted[MARK_PATH] = mark
    verify_runtime_contract(original, adapted)
    return adapted


def verify_runtime_contract(original, adapted):
    old, new = (json.loads(entries["manifest.json"]) for entries in (original, adapted))
    for manifest in (old, new):
        for key in ("name", "description", "homepage_url", "icons"):
            del manifest[key]
        for key in ("default_title", "default_icon"):
            del manifest["action"][key]
    if old != new:
        raise ValueError("connector branding changed permission or runtime authority")
    protected = re.compile(r"['\"`]((?:synon[._-]|SYNON_|needs_synon_|MAX_SYNON)[A-Za-z0-9_./-]*)")
    for path in original:
        if path.endswith(".js"):
            if protected.findall(original[path].decode()) != protected.findall(adapted[path].decode()):
                raise ValueError(f"connector wire/storage identity changed: {path}")
    if any(path in adapted for path in OLD_ICONS) or adapted[MARK_PATH][:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("connector icon contract changed")


def deterministic_archive(entries):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(entries):
            info = zipfile.ZipInfo(path, (1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = (0o40755 if path.endswith("/") else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, entries[path], compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
    return output.getvalue()
