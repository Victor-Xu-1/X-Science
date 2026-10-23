"""Projected PDBe experimental-entry validation through the shared EBI reader."""
from __future__ import annotations

from decimal import Decimal
import math
import re
from typing import Any, Callable

import anyio

from mcp_servers_common.ebi_http import ebi_json_session

PDB_ID_PATTERN = r"^[1-9][A-Za-z0-9]{3}$"
PDBE_INPUT_SCHEMA = {
    "type": "object",
    "properties": {"pdb_id": {"type": "string", "pattern": PDB_ID_PATTERN,
                              "maxLength": 4, "examples": ["1cbs"]}},
    "required": ["pdb_id"],
    "additionalProperties": False,
}
_API = "https://www.ebi.ac.uk/pdbe/api"
_MISSING = "Requested endpoint does not contain any data"
_INVALID = "PDBe returned invalid entry data"
_MAX_EXACT_INTEGER = 2**53 - 1
_STATS = ("bonds", "angles", "rna_suite", "rna_pucker",
          "protein_ramachandran", "protein_sidechains", "RSRZ")


def _require(valid: bool) -> None:
    if not valid:
        raise ValueError(_INVALID)


def _number(value: Any) -> bool:
    # JSON booleans are not numbers. Preserve the source's finite JSON-number
    # contract without silently rounding arbitrary Python integers for JS consumers.
    return (type(value) is int and abs(value) <= _MAX_EXACT_INTEGER
            or type(value) is float and math.isfinite(value))


def _text(value: Any, maximum: int) -> bool:
    # The reference's string caps count UTF-16 units, not Python code points.
    return isinstance(value, str) and len(value.encode("utf-16-le", "surrogatepass")) <= maximum * 2


def _summary(value: Any) -> dict:
    _require(isinstance(value, list) and len(value) == 1 and isinstance(value[0], dict))
    row = value[0]
    title, methods = row.get("title"), row.get("experimental_method")
    _require(_text(title, 4000))
    _require(isinstance(methods, list) and 1 <= len(methods) <= 20
             and all(_text(method, 200) for method in methods))
    return {"title": title, "experimental_method": list(methods)}


def _quality(value: Any) -> dict:
    _require(isinstance(value, dict))
    result = {}
    for name in ("geometry_quality", "data_quality", "overall_quality"):
        _require(name in value)
        score = value[name]
        _require(score is None or _number(score) and 0 <= score <= 100)
        result[name] = score
    _require("experiment_data_available" in value)
    available = value["experiment_data_available"]
    _require(available is None or type(available) is bool or available == "unknown")
    result["experiment_data_available"] = available
    return result


def _statistic(value: Any) -> dict:
    _require(isinstance(value, dict))
    result = {}
    if "rmsz" in value:
        rmsz = value["rmsz"]
        _require(rmsz is None or _number(rmsz) and rmsz >= 0)
        result["rmsz"] = rmsz
    for name in ("num_checked", "num_outliers"):
        count = value.get(name)
        _require(_number(count) and 0 <= count <= _MAX_EXACT_INTEGER and int(count) == count)
        result[name] = int(count)
    _require(result["num_outliers"] <= result["num_checked"] and "percent_outliers" in value)
    percent = value["percent_outliers"]
    _require(percent is None or isinstance(percent, str) and len(percent) <= 30
             and re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", percent) is not None
             and Decimal(percent) <= 100)
    result["percent_outliers"] = percent
    return result


def _statistics(value: Any) -> dict:
    _require(isinstance(value, dict))
    result = {name: _statistic(value[name]) for name in _STATS if name in value}
    _require(bool(result))
    return result


async def _entry_data(client: Any, url: str, pdb_id: str, project: Callable) -> Any:
    status, raw = await client.request("GET", url, allow_not_found=True)
    if status == 404:
        if isinstance(raw, dict) and raw.get("message") == _MISSING:
            return None
        raise ValueError("PDBe returned an unexpected not-found response")
    _require(isinstance(raw, dict) and pdb_id in raw)
    return project(raw[pdb_id])


async def _reports(client: Any, urls: dict, pdb_id: str) -> list:
    results = [None, None]

    async def read(index: int, name: str, project: Callable) -> None:
        results[index] = await _entry_data(client, urls[name], pdb_id, project)

    try:
        async with anyio.create_task_group() as tasks:
            tasks.start_soon(read, 0, "quality_scores", _quality)
            tasks.start_soon(read, 1, "validation_stats", _statistics)
    except ExceptionGroup as errors:
        # Match a failed combined read, not a partial success. Keep the original
        # typed failure for the shared transport's HTTP/timeout classification.
        raise errors.exceptions[0] from None
    return results


async def fetch_validation(pdb_id: str) -> dict[str, Any]:
    """One exact experimental ID; confirmed absence is distinct from failure."""
    if not isinstance(pdb_id, str) or re.fullmatch(PDB_ID_PATTERN, pdb_id) is None:
        raise ValueError("Provide one four-character PDB ID")
    pdb_id = pdb_id.lower()
    urls = {
        "entry": f"{_API}/pdb/entry/summary/{pdb_id}",
        "quality_scores": f"{_API}/validation/summary_quality_scores/entry/{pdb_id}",
        "validation_stats": f"{_API}/validation/key_validation_stats/entry/{pdb_id}",
    }
    async with ebi_json_session("PDBe") as client:
        entry = await _entry_data(client, urls["entry"], pdb_id, _summary)
        scores, statistics = (await _reports(client, urls, pdb_id)
                              if entry is not None else (None, None))
        return {
            "source": "PDBe", "pdb_id": pdb_id,
            "source_url": f"https://www.ebi.ac.uk/pdbe/entry/pdb/{pdb_id}",
            "entry_found": entry is not None, "entry": entry,
            "quality_scores": scores, "validation_stats": statistics, "source_urls": urls,
        }
