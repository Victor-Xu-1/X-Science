"""Document task-derived inputs without presenting them as predictor receipts."""
from __future__ import annotations

import hashlib
import json
import math
from pathlib import Path


def load_documented_input(path: Path, source: Path, group: str, values: dict[str, float]) -> dict:
    if path.stat().st_size > 1024 * 1024:
        raise ValueError("documented input exceeds size bound")
    record = json.loads(path.read_text(encoding="utf-8"))
    expected = {"schema", "evidence_group", "input_sha256", "basis", "method", "sources", "limitations", "values"}
    if not isinstance(record, dict) or set(record) != expected:
        raise ValueError("documented input must contain the documented provenance fields")
    if record["schema"] != "synon.documented-input.v1" or record["evidence_group"] != group:
        raise ValueError("documented input schema or group mismatch")
    if record["basis"] not in {"literature-guided", "structure-derived", "user-supplied", "exploratory"}:
        raise ValueError("unknown documented input basis")
    for key in ("method", "limitations"):
        if not isinstance(record[key], str) or not record[key].strip() or len(record[key].encode()) > 16384 or "\0" in record[key]:
            raise ValueError("documented input requires a method and limitations")
    references = record["sources"]
    if not isinstance(references, list) or not 1 <= len(references) <= 64 or any(
        not isinstance(ref, str) or not ref.strip() or len(ref.encode()) > 4096 or "\0" in ref for ref in references
    ):
        raise ValueError("documented input requires source references")
    digest = hashlib.sha256()
    with source.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    if record["input_sha256"] != digest.hexdigest():
        raise ValueError("documented input does not match the current source bytes")
    declared = record["values"]
    if not isinstance(declared, dict) or set(declared) != set(values):
        raise ValueError("documented input parameter group is incomplete")
    for key, value in values.items():
        actual = declared[key]
        if isinstance(actual, bool) or not isinstance(actual, (int, float)) or not math.isfinite(actual) or actual != value:
            raise ValueError("documented input values do not match execution parameters")
    return record
