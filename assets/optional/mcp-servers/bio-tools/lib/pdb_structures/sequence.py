"""Experimental PDB protein entity search with bounded scans and honest coverage."""
# Adapted from AIPOCH Open Science80a262e structures-pdb.ts; modified for
# native-async Python, strict validation and shared bounded scan/retry budgets.
# Apache-2.0 attribution: docs/licenses/open-science-pdb-search-80a262e/NOTICE.md.
from __future__ import annotations

import re
from typing import Any

from .async_client import rcsb_json_session
from .search_validation import integer_value, number_value, object_value
from .sequence_records import entity_identity, project_entity, project_hit

PAGE_ROWS = 100


def _protein_sequence(value: object) -> str:
    if not isinstance(value, str) or len(value) > 50000:
        raise ValueError("sequence must be one protein sequence of at most 50000 input characters")
    lines = value.strip().splitlines()
    if lines and lines[0].startswith(">"):
        lines.pop(0)
    sequence = re.sub(r"\s+", "", "".join(lines)).upper()
    if not 25 <= len(sequence) <= 10000 or not re.fullmatch(r"[ACDEFGHIKLMNPQRSTVWYBXZJUO]+", sequence):
        raise ValueError("sequence must contain one ungapped protein sequence of 25..10000 amino acids")
    return sequence


def _query(sequence: str, identity: float, evalue: float, start: int, rows: int) -> dict:
    return {"query": {"type": "group", "logical_operator": "and", "nodes": [
        {"type": "terminal", "service": "sequence", "parameters": {
            "value": sequence, "sequence_type": "protein",
            "identity_cutoff": identity, "evalue_cutoff": evalue}},
        {"type": "terminal", "service": "text", "parameters": {
            "attribute": "rcsb_entry_info.structure_determination_methodology",
            "operator": "exact_match", "value": "experimental"}}]},
        "return_type": "polymer_entity", "request_options": {
            "paginate": {"start": start, "rows": rows},
            "results_content_type": ["experimental"], "results_verbosity": "verbose"}}


async def search_sequence(sequence: str, *, identity_cutoff: float = 0.3,
                          evalue_cutoff: float = 0.1, min_query_coverage: float = 0,
                          max_candidates: int = 100, max_rows: int = 10) -> dict[str, Any]:
    sequence = _protein_sequence(sequence)
    number_value(identity_cutoff, "identity_cutoff", 0, 1)
    number_value(evalue_cutoff, "evalue_cutoff", 0)
    if evalue_cutoff == 0:
        raise ValueError("evalue_cutoff must be positive")
    number_value(min_query_coverage, "min_query_coverage", 0, 1)
    integer_value(max_candidates, "max_candidates", 1, 1000)
    integer_value(max_rows, "max_rows", 1, 25)
    hits, seen, total_count = [], set(), 0
    async with rcsb_json_session("sequence", max_candidates=max_candidates, max_rows=max_rows) as session:
        # A provider returning short pages cannot amplify a bounded candidate scan
        # into hundreds of network requests. Any unfinished scan remains truncated.
        for _ in range((max_candidates + PAGE_ROWS - 1) // PAGE_ROWS):
            rows = min(PAGE_ROWS, max_candidates - len(hits))
            raw = await session.post_search(
                _query(sequence, identity_cutoff, evalue_cutoff, len(hits), rows))
            if raw is None:
                break
            body = object_value(raw, "sequence response")
            if body.get("result_type", "polymer_entity") != "polymer_entity":
                raise ValueError("RCSB returned the wrong sequence result type")
            total_count = integer_value(body.get("total_count"), "sequence total_count", 0)
            page = body.get("result_set", [])
            if not isinstance(page, list) or total_count < len(hits) + min(len(page), rows):
                raise ValueError("RCSB returned inconsistent sequence-search counts")
            for value in page[:rows]:
                hit = object_value(value, "sequence hit")
                entity_identity(hit.get("identifier"))
                if hit["identifier"] in seen:
                    raise ValueError("RCSB returned duplicate sequence-search hits")
                seen.add(hit["identifier"])
                hits.append(hit)
            if len(hits) >= min(total_count, max_candidates) or not page:
                break

        records, n_matched = [], 0
        for hit in hits:
            session.require_deadline()
            record = project_hit(hit, len(sequence), min_query_coverage)
            if record is None:
                continue
            n_matched += 1
            if len(records) >= max_rows:
                continue
            metadata = await session.get_polymer_entity(record["pdb_id"], record["entity_id"])
            if metadata is None:
                record.update({"metadata_error": "not_found", "description": None,
                               "asym_ids": None, "auth_asym_ids": None, "sequence_length": None})
            else:
                record.update(project_entity(metadata, record["pdb_id"], record["entity_id"]))
            records.append(record)
        return {"query_length": len(sequence), "identity_cutoff": identity_cutoff,
                "evalue_cutoff": evalue_cutoff, "min_query_coverage": min_query_coverage,
                "total_count": total_count, "n_scanned": len(hits), "n_matched": n_matched,
                "n_returned": len(records), "max_candidates": max_candidates, "max_rows": max_rows,
                "truncated": total_count > len(hits) or n_matched > len(records), "records": records}
