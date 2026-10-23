"""Exact public UniChem identifier mapping with bounded, literal projection."""
from __future__ import annotations

import math
import re

from mcp_servers_common.ebi_http import ebi_json_session

API = "https://www.ebi.ac.uk/unichem/api/v1/compounds"
INCHIKEY_PATTERN = r"[A-Z]{14}-[A-Z]{10}-[A-Z]"
CHEMBL_PATTERN = r"CHEMBL[1-9][0-9]{0,14}"
IDENTIFIER_PATTERN = rf"^(?:{INCHIKEY_PATTERN}|{CHEMBL_PATTERN})$"
UNICHEM_INPUT_SCHEMA = {
    "type": "object",
    "properties": {
        "id_type": {"type": "string", "enum": ["inchikey", "chembl"], "examples": ["chembl"]},
        "identifier": {"type": "string", "maxLength": 27, "pattern": IDENTIFIER_PATTERN,
                       "examples": ["CHEMBL1089"]},
    },
    "required": ["id_type", "identifier"],
    "additionalProperties": False,
    "allOf": [{
        "if": {"properties": {"id_type": {"const": "inchikey"}}},
        "then": {"properties": {"identifier": {"type": "string", "pattern": rf"^{INCHIKEY_PATTERN}$"}}},
        "else": {"properties": {"identifier": {"type": "string", "pattern": rf"^{CHEMBL_PATTERN}$"}}},
    }],
    "examples": [
        {"id_type": "chembl", "identifier": "CHEMBL1089"},
        {"id_type": "inchikey", "identifier": "RMUCZJUITONUFY-UHFFFAOYSA-N"},
    ],
}
_INCHIKEY = re.compile(INCHIKEY_PATTERN)
_CHEMBL = re.compile(CHEMBL_PATTERN)
_SAFE_INTEGER = (1 << 53) - 1
_SOURCES = (
    (1, "ChEMBL", _CHEMBL, "https://www.ebi.ac.uk/chembl/compound_report_card/"),
    (7, "ChEBI", re.compile(r"CHEBI:[1-9][0-9]{0,14}"), "https://www.ebi.ac.uk/chebi/"),
    (22, "PubChem", re.compile(r"[1-9][0-9]{0,14}"), "https://pubchem.ncbi.nlm.nih.gov/compound/"),
)
_SOURCE_BY_ID = {entry[0]: entry for entry in _SOURCES}


def _integer(value: object, *, minimum: int) -> int:
    # JSON booleans are not numbers. Integral JSON floats are accepted only
    # while exact and interoperable with the reference's JavaScript numbers.
    if type(value) not in (int, float):
        raise ValueError("UniChem returned invalid compound data")
    if isinstance(value, float) and (not math.isfinite(value) or not value.is_integer()):
        raise ValueError("UniChem returned invalid compound data")
    if not minimum <= value <= _SAFE_INTEGER:
        raise ValueError("UniChem returned invalid compound data")
    return int(value)


def _query_payload(id_type: object, identifier: object) -> dict:
    pattern = _INCHIKEY if id_type == "inchikey" else _CHEMBL
    if (id_type not in ("inchikey", "chembl") or not isinstance(identifier, str)
            or len(identifier) > 27 or pattern.fullmatch(identifier) is None):
        raise ValueError("Provide one exact full InChIKey or ChEMBL ID")
    if id_type == "inchikey":
        return {"compound": identifier, "type": "inchikey"}
    return {"compound": identifier, "sourceID": 1, "type": "sourceID"}


def _source_mappings(sources: object, identifier: str, id_type: str) -> tuple[list[dict], int]:
    if not isinstance(sources, list) or len(sources) > 10_000:
        raise ValueError("UniChem returned invalid compound data")
    mappings: list[dict] = []
    requested_source_present = False
    for source in sources:
        if not isinstance(source, dict):
            raise ValueError("UniChem returned invalid compound data")
        source_id = _integer(source.get("id"), minimum=1)
        compound_id = source.get("compoundId")
        # Match the reference's 1,000 UTF-16 code-unit native string bound.
        if (not isinstance(compound_id, str) or not 1 <= len(compound_id) <= 1000
                or len(compound_id.encode("utf-16-le", "surrogatepass")) > 2000):
            raise ValueError("UniChem returned invalid compound data")
        if source_id == 1 and compound_id == identifier:
            requested_source_present = True
        database = _SOURCE_BY_ID.get(source_id)
        if database is None:
            continue
        _, name, pattern, base_url = database
        if pattern.fullmatch(compound_id) is None:
            raise ValueError("UniChem returned an invalid mapped identifier")
        # Never accept upstream URLs, even for a recognized database.
        mappings.append({"source": name, "identifier": compound_id, "url": base_url + compound_id})
    if id_type == "chembl" and not requested_source_present:
        raise ValueError("UniChem returned a compound inconsistent with the requested identifier")
    return mappings, len(sources)


def _project_response(raw: object, id_type: str, identifier: str) -> dict:
    if not isinstance(raw, dict) or raw.get("response") not in ("Success", "Not found"):
        raise ValueError("UniChem returned invalid compound data")
    compounds = raw.get("compounds")
    if not isinstance(compounds, list) or len(compounds) > 100:
        raise ValueError("UniChem returned invalid compound data")
    total = raw.get("totalCompounds")
    if "totalCompounds" in raw:
        if isinstance(total, str):
            if re.fullmatch(r"[0-9]{1,10}", total) is None:
                raise ValueError("UniChem returned invalid compound data")
            total = int(total)
        else:
            total = _integer(total, minimum=0)
    if ((raw["response"] == "Not found") != (len(compounds) == 0)
            or (raw["response"] == "Success" and "totalCompounds" not in raw)
            or ("totalCompounds" in raw and total != len(compounds))):
        raise ValueError("UniChem returned inconsistent match counts")
    records = []
    # Validate and count every native row before applying either output cap.
    for row in compounds:
        if not isinstance(row, dict):
            raise ValueError("UniChem returned invalid compound data")
        uci = _integer(row.get("uci"), minimum=1)
        key = row.get("standardInchiKey")
        if not isinstance(key, str) or _INCHIKEY.fullmatch(key) is None:
            raise ValueError("UniChem returned invalid compound data")
        if id_type == "inchikey" and key != identifier:
            raise ValueError("UniChem returned a compound inconsistent with the requested identifier")
        mappings, total_mappings = _source_mappings(row.get("sources"), identifier, id_type)
        records.append({
            "uci": uci,
            "standard_inchi_key": key,
            "total_mappings": total_mappings,
            "filtered_out_mappings": total_mappings - len(mappings),
            "matching_mappings": len(mappings),
            "mappings_truncated": len(mappings) > 20,
            "mappings": mappings[:20],
        })
    return {
        "source": "UniChem", "source_url": API,
        "id_type": id_type, "identifier": identifier,
        "target_sources": [entry[1] for entry in _SOURCES],
        "total_matches": len(records), "records_truncated": len(records) > 5,
        "records": records[:5],
    }


async def map_compound(id_type: str, identifier: str) -> dict:
    """Map one exact identifier; transport failures never become empty matches."""
    payload = _query_payload(id_type, identifier)
    async with ebi_json_session("UniChem") as client:
        _, raw = await client.request("POST", API, json=payload)
        return _project_response(raw, id_type, identifier)
