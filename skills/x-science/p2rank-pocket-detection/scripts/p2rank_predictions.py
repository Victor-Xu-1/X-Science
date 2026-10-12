"""Parse and retain verified P2Rank engine output without inventing candidates."""

from __future__ import annotations

import csv
import hashlib
import math
from pathlib import Path
import re


class NoPocketCandidates(ValueError):
    code = "p2rank_no_pockets"
    retryable = False
    recovery = (
        "Review the retained raw predictions, engine log, and input structure. "
        "Do not repeat unchanged input/profile or construct replacement coordinates. "
        "A different input or method requires its own scientific justification."
    )

    def __init__(self) -> None:
        super().__init__(
            "P2Rank returned zero candidate pockets for this input/profile; "
            "this is not a rank-format error and no docking box can be issued. "
            + self.recovery
        )


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def finite_float(row: dict[str, str], key: str) -> float:
    try:
        value = float(row[key])
    except (KeyError, TypeError, ValueError):
        raise ValueError(f"P2Rank output is missing a numeric {key} value") from None
    if not math.isfinite(value):
        raise ValueError(f"P2Rank output contains a non-finite {key} value")
    return value


def parse_predictions(path: Path) -> list[dict[str, object]]:
    rows: list[dict[str, object]] = []
    required = {"rank", "score", "probability", "center_x", "center_y", "center_z", "surf_atom_ids"}
    with path.open("r", encoding="utf-8-sig", newline="") as handle:
        reader = csv.DictReader(handle, skipinitialspace=True)
        fields = [str(value).strip() for value in (reader.fieldnames or [])]
        if not required.issubset(fields) or len(set(fields)) != len(fields):
            raise ValueError("P2Rank predictions CSV has missing or duplicate header fields")
        for raw in reader:
            normalized = {
                str(key).strip(): str(value).strip()
                for key, value in raw.items()
                if key is not None and value is not None
            }
            try:
                rank = int(normalized["rank"])
            except (KeyError, ValueError):
                raise ValueError("P2Rank output is missing an integer rank") from None
            probability = finite_float(normalized, "probability")
            if probability < 0 or probability > 1:
                raise ValueError("P2Rank probability is outside [0, 1]")
            atom_ids = [int(value) for value in re.findall(r"\d+", normalized.get("surf_atom_ids", ""))]
            rows.append({
                "name": normalized.get("name", f"pocket{rank}"),
                "rank": rank, "score": finite_float(normalized, "score"),
                "probability": probability,
                "center": [finite_float(normalized, key) for key in ("center_x", "center_y", "center_z")],
                "residue_ids": normalized.get("residue_ids", ""),
                "surface_atom_ids": sorted(set(atom_ids)),
            })
    if not rows:
        raise NoPocketCandidates()
    rows.sort(key=lambda row: int(row["rank"]))
    if [int(row["rank"]) for row in rows] != list(range(1, len(rows) + 1)):
        raise ValueError("P2Rank output must contain contiguous rank-ordered pockets")
    return rows


def prediction_failure_record(error: Exception, pack_id: str, directory: Path) -> dict[str, object]:
    record: dict[str, object] = {
        "error": str(error), "execution_pack_id": pack_id, "overall_pass": False,
        "code": getattr(error, "code", "p2rank_execution_failed"),
    }
    if isinstance(error, NoPocketCandidates):
        record.update(candidate_count=0, retryable=error.retryable, recovery=error.recovery)
    evidence = {}
    for name in ("p2rank_predictions.csv", "p2rank_params.txt", "p2rank.log"):
        path = directory / name
        if path.is_file() and not path.is_symlink():
            evidence[name] = {"bytes": path.stat().st_size, "sha256": sha256_file(path)}
    record["evidence"] = evidence
    return record
