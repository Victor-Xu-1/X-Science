"""Experimental deposited-chain structure-informed similarity, without alignment."""
# Adapted from AIPOCH Open Science80a262e structures-pdb-similarity.ts;
# modified for the existing Python MCP/async transport and stricter JSON reads.
# Apache-2.0 attribution: docs/licenses/open-science-pdb-search-80a262e/NOTICE.md.
from __future__ import annotations

import re
from typing import Any

from .async_client import rcsb_json_session
from .client import SEARCH_URL
from .search_validation import integer_value, number_value, object_value

_PDB_ID = re.compile(r"[1-9][A-Za-z0-9]{3}", re.ASCII)
_CHAIN_ID = re.compile(r"[A-Za-z0-9]{1,16}", re.ASCII)
_RESULT_CHAIN_ID = re.compile(r"[A-Za-z0-9]+", re.ASCII)


def _hit(value: object) -> dict:
    hit = object_value(value, "structure hit")
    identifier = hit.get("identifier")
    parts = identifier.rsplit(".", 1) if isinstance(identifier, str) else []
    if len(parts) != 2 or not _PDB_ID.fullmatch(parts[0]) or not _RESULT_CHAIN_ID.fullmatch(parts[1]):
        raise ValueError("RCSB returned an invalid polymer-instance identifier")
    services = hit.get("services")
    if not isinstance(services, list) or len(services) != 1:
        raise ValueError("RCSB structure hit requires one structure service")
    service = object_value(services[0], "structure service")
    nodes = service.get("nodes")
    if service.get("service_type") != "structure" or not isinstance(nodes, list) or len(nodes) != 1:
        raise ValueError("RCSB structure hit requires one structure score")
    node = object_value(nodes[0], "structure score")
    return {"identifier": identifier, "pdb_id": parts[0], "label_asym_id": parts[1],
            "relevance_score": number_value(hit.get("score"), "structure relevance score", -float("inf")),
            "raw_structure_score": number_value(node.get("original_score"), "structure score", -float("inf"))}


async def search_structure_similarity(pdb_id: str, label_asym_id: str, *,
                                      max_rows: int = 10) -> dict[str, Any]:
    if not isinstance(pdb_id, str) or not _PDB_ID.fullmatch(pdb_id):
        raise ValueError("pdb_id must be a four-character deposited PDB identifier")
    if not isinstance(label_asym_id, str) or not _CHAIN_ID.fullmatch(label_asym_id):
        raise ValueError("label_asym_id must contain 1..16 alphanumeric characters")
    integer_value(max_rows, "max_rows", 1, 50)
    pdb_id = pdb_id.upper()
    query = {"pdb_id": pdb_id, "label_asym_id": label_asym_id,
             "similarity_type": "global", "ptmscore_cutoff": 0.8, "number_of_candidates": 10000}
    async with rcsb_json_session("structure") as session:
        raw = await session.post_search({
            "query": {"type": "terminal", "service": "structure", "parameters": {
                "value": {"entry_id": pdb_id, "asym_id": label_asym_id},
                "target_search_space": "polymer_entity_instance", "similarity_type": "global",
                "ptmscore_cutoff": 0.8, "number_of_candidates": 10000}},
            "return_type": "polymer_instance", "request_options": {
                "paginate": {"start": 0, "rows": max_rows},
                "results_content_type": ["experimental"], "results_verbosity": "verbose"}})
        if raw is None:
            total_count, hits = 0, []
        else:
            body = object_value(raw, "structure response")
            if body.get("result_type") != "polymer_instance":
                raise ValueError("RCSB returned the wrong structure result type")
            total_count = integer_value(body.get("total_count"), "structure total_count", 0)
            values = body.get("result_set", [])
            if not isinstance(values, list) or total_count < len(values) or total_count > 0 and not values:
                raise ValueError("RCSB returned inconsistent structure-search counts")
            hits = [_hit(value) for value in values]
            if len({hit["identifier"] for hit in hits}) != len(hits):
                raise ValueError("RCSB returned duplicate structure-search hits")
        records = hits[:max_rows]
        return {"source": "RCSB PDB", "source_url": SEARCH_URL, "query": query,
                "total_count": total_count, "n_returned": len(records),
                "truncated": total_count > len(records),
                "score_note": "RCSB embedding search with global matching and predicted TM-score "
                              "cutoff 0.8 among 10,000 candidates. Scores are upstream ranking values, "
                              "not a returned structural alignment or RMSD; hit counts are not exhaustive.",
                "records": records}
