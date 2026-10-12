"""Resolve immutable imported source paths to the current product namespace.

The input names below are genuine original-import facts, not active component
identities. Keep the original SOURCE_IMPORT_MANIFEST byte-identical. Approval
fingerprints bind both the actual target bytes and its original source path.
"""
from pathlib import PurePosixPath
import re


def current_source_path(original: str) -> str:
    if (not isinstance(original, str) or not original or "\\" in original
            or PurePosixPath(original).is_absolute()
            or any(part in {"", ".", ".."} for part in original.split("/"))):
        raise ValueError("invalid original frontend source path")
    result = original
    for previous, current in (
        ("synon-biomed", "x-science"), ("synon-ai", "x-science"),
        ("SynonBiomed", "XScience"), ("synonBiomed", "xScience"),
        ("synonbiomed", "xscience"), ("SynonAI", "XScience"),
        ("SynonAi", "XScience"), ("Synon", "XScience"),
        ("synon", "xscience"),
    ):
        result = re.sub(re.escape(previous) + r"(?!ym)", current, result)
    return result


def canonical_source_records(source: dict) -> dict:
    records = {}
    for record in source.get("files", []):
        original = record["path"]
        target = current_source_path(original)
        if target in records:
            raise ValueError("original frontend paths collide in current namespace: " + target)
        records[target] = record
    return records
