"""Strict identity and alignment projection for RCSB sequence hits."""
# Adapted from AIPOCH Open Science80a262e structures-pdb.ts; modified for
# Python identity/metric validation while retaining coverage and null semantics.
# Apache-2.0 attribution: docs/licenses/open-science-pdb-search-80a262e/NOTICE.md.
from __future__ import annotations

import re

from .search_validation import integer_value, number_value, object_value

_ENTRY_ID = re.compile(r"(?:[1-9][A-Za-z0-9]{3}|[pP][dD][bB]_[A-Za-z0-9]{8})", re.ASCII)
_OPTIONAL_METRICS = ("alignment_length", "mismatches", "gaps_opened", "query_beg",
                     "query_end", "subject_beg", "subject_end")


def entity_identity(value: object) -> tuple[str, str]:
    if not isinstance(value, str):
        raise ValueError("RCSB returned an invalid polymer entity identifier")
    parts = value.rsplit("_", 1)
    if (len(parts) != 2 or not _ENTRY_ID.fullmatch(parts[0])
            or not re.fullmatch(r"[1-9][0-9]*", parts[1], re.ASCII)):
        raise ValueError("RCSB returned an invalid polymer entity identifier")
    return parts[0], parts[1]


def _match(value: object, query_length: int) -> dict:
    raw = object_value(value, "sequence alignment")
    result = {
        "sequence_identity": number_value(raw.get("sequence_identity"), "sequence_identity", 0, 1),
        "evalue": number_value(raw.get("evalue"), "evalue"),
        "bitscore": number_value(raw.get("bitscore"), "bitscore"),
    }
    for key in _OPTIONAL_METRICS:
        value = raw.get(key)
        result[key] = None if value is None else integer_value(
            value, key, 0 if key in ("mismatches", "gaps_opened") else 1)
    begin, end = result["query_beg"], result["query_end"]
    result["query_coverage"] = (
        (end - begin + 1) / query_length
        if begin is not None and end is not None and begin <= end <= query_length else None)
    return result


def project_hit(value: object, query_length: int, coverage: float) -> dict | None:
    raw = object_value(value, "sequence hit")
    pdb_id, entity_id = entity_identity(raw.get("identifier"))
    score = raw.get("score")
    if score is not None:
        number_value(score, "sequence relevance score", -float("inf"))
    services = raw.get("services")
    if not isinstance(services, list):
        raise ValueError("RCSB sequence hit is missing match context")
    contexts = []
    for value in services:
        service = object_value(value, "sequence service")
        if service.get("service_type") != "sequence":
            continue
        nodes = service.get("nodes", [])
        if not isinstance(nodes, list):
            raise ValueError("Invalid RCSB sequence nodes")
        for value in nodes:
            node = object_value(value, "sequence node")
            matches = node.get("match_context", [])
            if not isinstance(matches, list):
                raise ValueError("Invalid RCSB sequence match context")
            contexts.extend(matches)
    if not contexts:
        raise ValueError("RCSB sequence hit is missing match context")
    matches = [_match(value, query_length) for value in contexts]
    matches = [match for match in matches if coverage == 0 or
               match["query_coverage"] is not None and match["query_coverage"] >= coverage]
    if not matches:
        return None
    return {"rcsb_id": raw["identifier"], "pdb_id": pdb_id, "entity_id": entity_id,
            "score": score, "matches": matches}


def project_entity(value: object, pdb_id: str, entity_id: str) -> dict:
    raw = object_value(value, "sequence entity metadata")
    ids = object_value(raw.get("rcsb_polymer_entity_container_identifiers"), "sequence entity identity")
    if (ids.get("entry_id") != pdb_id or ids.get("entity_id") != entity_id
            or raw.get("rcsb_id") is not None and raw["rcsb_id"] != f"{pdb_id}_{entity_id}"):
        raise ValueError("RCSB sequence entity metadata does not match the requested identity")
    for key in ("asym_ids", "auth_asym_ids"):
        chains = ids.get(key)
        if not isinstance(chains, list) or any(not isinstance(v, str) or not v.strip() for v in chains):
            raise ValueError(f"Invalid RCSB sequence entity {key}")
    entity_raw, polymer_raw = raw.get("rcsb_polymer_entity"), raw.get("entity_poly")
    entity = {} if entity_raw is None else object_value(entity_raw, "sequence entity")
    polymer = {} if polymer_raw is None else object_value(polymer_raw, "sequence polymer")
    description, length = entity.get("pdbx_description"), polymer.get("rcsb_sample_sequence_length")
    if description is not None and not isinstance(description, str):
        raise ValueError("Invalid RCSB sequence entity description")
    if length is not None:
        integer_value(length, "sequence_length", 0)
    return {"description": description, "asym_ids": ids["asym_ids"],
            "auth_asym_ids": ids["auth_asym_ids"], "sequence_length": length}
