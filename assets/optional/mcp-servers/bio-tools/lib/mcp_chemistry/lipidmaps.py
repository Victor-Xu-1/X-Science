"""Bounded exact LIPID MAPS retrieval for the existing Chemistry connector."""
from __future__ import annotations

import json
import re

import anyio
import httpx


LMID_PATTERN = r"LM(?:FA|GL|GP|SP|ST|PR|SL|PK)[0-9]{4}(?:[0-9]{2})?[A-Z0-9]{4}"
CID_PATTERN = r"[1-9][0-9]{0,14}"
MAX_RESPONSE_BYTES = 100_000
CALL_TIMEOUT_SECONDS = 25.0
MAX_ROWS = 100
DISPLAY_ROWS = 5
SOURCE_ROOT = "https://www.lipidmaps.org"
_TEXT_FIELDS = (
    "name", "sys_name", "abbrev", "abbrev_chains", "sub_class", "class_level4",
    "formula", "inchi", "inchi_key", "smiles", "kegg_id", "hmdb_id", "chebi_id",
    "lipidbank_id", "metacyc_id",
)


def _validate_identifier(id_type: str, identifier: str) -> None:
    pattern = {"lm_id": LMID_PATTERN, "pubchem_cid": CID_PATTERN}.get(id_type) if isinstance(id_type, str) else None
    if pattern is None or not isinstance(identifier, str) or not re.fullmatch(pattern, identifier):
        raise ValueError("Provide one exact LIPID MAPS ID or positive PubChem CID string")


def _unique_object(pairs: list[tuple[str, object]]) -> dict:
    result: dict = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("LIPID MAPS returned duplicate JSON fields")
        result[key] = value
    return result


def _reject_constant(_value: str) -> None:
    raise ValueError("LIPID MAPS returned a non-finite JSON number")


def _record(row: object) -> dict:
    if not isinstance(row, dict):
        raise ValueError("LIPID MAPS returned invalid compound data")
    lmid = row.get("lm_id")
    if not isinstance(lmid, str) or not re.fullmatch(LMID_PATTERN, lmid):
        raise ValueError("LIPID MAPS returned invalid compound data")
    result: dict = {"lm_id": lmid}
    for key in ("core", "main_class"):
        value = row.get(key)
        if not isinstance(value, str) or not 1 <= len(value) <= 200:
            raise ValueError("LIPID MAPS returned invalid compound data")
        result[key] = value
    for key in (*_TEXT_FIELDS, "pubchem_cid", "exactmass"):
        if key not in row:
            continue
        value = row[key]
        if value is not None:
            if not isinstance(value, str) or len(value) > 2000:
                raise ValueError("LIPID MAPS returned invalid compound data")
            if key == "pubchem_cid" and not re.fullmatch(CID_PATTERN, value):
                raise ValueError("LIPID MAPS returned invalid compound data")
            if key == "exactmass" and (len(value) > 30 or not re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", value)):
                raise ValueError("LIPID MAPS returned invalid compound data")
        result[key] = value
    result["record_url"] = f"{SOURCE_ROOT}/databases/lmsd/{lmid}"
    return result


def _parse(raw: object, id_type: str, identifier: str, url: str) -> dict:
    if isinstance(raw, list) and not raw:
        rows: list = []
    elif isinstance(raw, dict) and "lm_id" in raw:
        rows = [raw]
    elif isinstance(raw, dict) and raw and all(re.fullmatch(r"Row[1-9][0-9]*", key) for key in raw):
        rows = list(raw.values())
    else:
        raise ValueError("LIPID MAPS returned an invalid response")
    if len(rows) > MAX_ROWS:
        raise ValueError("LIPID MAPS returned too many compound records")
    # Validate every row before the display cap, including omitted matches.
    records = [_record(row) for row in rows]
    if any(row.get(id_type) != identifier for row in records):
        raise ValueError("LIPID MAPS returned a compound inconsistent with the requested identifier")
    return {
        "source": "LIPID MAPS", "license": "CC BY 4.0",
        "license_url": f"{SOURCE_ROOT}/terms-of-use", "source_url": url,
        "id_type": id_type, "identifier": identifier,
        "total_matches": len(records), "records_truncated": len(records) > DISPLAY_ROWS,
        "records": records[:DISPLAY_ROWS],
    }


def _http_client() -> httpx.AsyncClient:
    # Keep the host's inherited proxy and CA policy. A fresh client carries
    # no browser/session cookies, explicit credentials or provider redirects.
    return httpx.AsyncClient(
        follow_redirects=False, trust_env=True,
        timeout=httpx.Timeout(CALL_TIMEOUT_SECONDS),
        headers={"Accept": "application/json", "Accept-Encoding": "identity",
                 "User-Agent": "X-Science/lipidmaps"},
    )


async def get_compound(id_type: str, identifier: str) -> dict:
    """Resolve one exact identifier; cancellation does not leave a worker thread."""
    _validate_identifier(id_type, identifier)
    url = f"{SOURCE_ROOT}/rest/compound/{id_type}/{identifier}/all"
    client = _http_client()
    try:
        with anyio.fail_after(CALL_TIMEOUT_SECONDS):
            async with client.stream("GET", url) as response:
                response.raise_for_status()
                # Refuse unexpected compression rather than expanding an
                # untrusted compressed body beyond the streaming byte budget.
                if response.headers.get("content-encoding", "identity").lower().strip() != "identity":
                    raise ValueError("LIPID MAPS returned an unsupported content encoding")
                body = bytearray()
                async for chunk in response.aiter_raw():
                    if len(body) + len(chunk) > MAX_RESPONSE_BYTES:
                        raise ValueError("LIPID MAPS response exceeds the byte budget")
                    body.extend(chunk)
            try:
                raw = json.loads(body.decode("utf-8"), object_pairs_hook=_unique_object,
                                 parse_constant=_reject_constant)
            except (UnicodeError, ValueError) as exc:
                raise ValueError("LIPID MAPS returned invalid JSON") from exc
            return _parse(raw, id_type, identifier, url)
    except (httpx.TimeoutException, TimeoutError) as exc:
        raise TimeoutError("LIPID MAPS query exceeded its whole-call deadline; retry later") from exc
    except httpx.HTTPStatusError as exc:
        raise RuntimeError(f"LIPID MAPS request failed with HTTP {exc.response.status_code}") from exc
    except httpx.HTTPError as exc:
        raise RuntimeError("LIPID MAPS upstream is unavailable") from exc
    finally:
        # Also close the live socket when caller cancellation is already
        # active; cleanup itself is bounded, never an unbounded task retry.
        with anyio.move_on_after(1, shield=True):
            await client.aclose()
