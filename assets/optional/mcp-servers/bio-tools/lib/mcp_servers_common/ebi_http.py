"""Bounded public EBI reads for the PDBe and UniChem adapters, not a task runtime."""
from __future__ import annotations

from contextlib import asynccontextmanager
import json
import math
from urllib.parse import urlsplit

import anyio
import httpx

CALL_TIMEOUT_SECONDS = 25.0
_SERVICES = {"PDBe": (100_000, 3), "UniChem": (1_000_000, 1)}
MAX_JSON_DEPTH = 64


def _unique_object(pairs: list[tuple[str, object]]) -> dict:
    result: dict = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON fields")
        result[key] = value
    return result


def _reject_constant(_value: str) -> None:
    raise ValueError("non-finite JSON number")


def _finite_float(value: str) -> float:
    number = float(value)
    if not math.isfinite(number):
        raise ValueError("non-finite JSON number")
    return number


def _decode_body(body: bytearray, service: str) -> object:
    try:
        # A byte cap alone does not bound a decoder's nesting/stack work.
        # This is only a depth preflight; the standard decoder still owns JSON
        # syntax, Unicode, duplicate keys and numeric validity.
        depth, in_string, escaped = 0, False, False
        for value in body:
            if in_string:
                if escaped:
                    escaped = False
                elif value == 92:
                    escaped = True
                elif value == 34:
                    in_string = False
            elif value == 34:
                in_string = True
            elif value in (91, 123):
                depth += 1
                if depth > MAX_JSON_DEPTH:
                    raise ValueError("JSON nesting exceeds the decode budget")
            elif value in (93, 125):
                depth -= 1
        return json.loads(body.decode("utf-8"), object_pairs_hook=_unique_object,
                          parse_constant=_reject_constant, parse_float=_finite_float)
    except (UnicodeError, ValueError, RecursionError) as exc:
        raise ValueError(f"{service} returned invalid JSON") from exc


def _http_client() -> httpx.AsyncClient:
    # Keep operator-owned proxy, CA and TLS posture; never acquire browser
    # cookies/credentials, relax verification or follow provider redirects.
    return httpx.AsyncClient(
        follow_redirects=False, trust_env=True, timeout=httpx.Timeout(CALL_TIMEOUT_SECONDS),
        headers={"Accept": "application/json", "Accept-Encoding": "identity",
                 "User-Agent": "X-Science/ebi-exact-lookup"},
    )


class _EBIJSONSession:
    def __init__(self, client: httpx.AsyncClient, service: str):
        self.client = client
        self.service = service
        self.byte_limit, self.request_limit = _SERVICES[service]
        self.requests = 0

    async def request(self, method: str, url: str, *, json: dict | None = None,
                      allow_not_found: bool = False) -> tuple[int, object]:
        parsed = urlsplit(url)
        endpoint_allowed = (
            self.service == "PDBe" and method == "GET" and parsed.path.startswith("/pdbe/api/")
            or self.service == "UniChem" and method == "POST" and parsed.path == "/unichem/api/v1/compounds"
        )
        if (not endpoint_allowed or parsed.scheme != "https" or parsed.netloc != "www.ebi.ac.uk"
                or parsed.query or parsed.fragment or url != url.strip() or any(c in url for c in "\r\n\t")):
            raise ValueError("EBI adapter endpoint is not supported")
        if allow_not_found and self.service != "PDBe":
            raise ValueError("Only explicit PDBe absence envelopes are supported")
        self.requests += 1
        if self.requests > self.request_limit:
            raise RuntimeError(f"{self.service} request count exceeds the call budget")
        async with self.client.stream(method, url, json=json) as response:
            if not (allow_not_found and response.status_code == 404):
                response.raise_for_status()
            if response.headers.get("content-encoding", "identity").strip().lower() != "identity":
                raise ValueError(f"{self.service} returned an unsupported content encoding")
            body = bytearray()
            async for chunk in response.aiter_raw():
                if len(body) + len(chunk) > self.byte_limit:
                    raise ValueError(f"{self.service} response exceeds the byte budget")
                body.extend(chunk)
        return response.status_code, _decode_body(body, self.service)


@asynccontextmanager
async def ebi_json_session(service: str):
    """One whole-call deadline spans all reads, parsing and caller projection."""
    if service not in _SERVICES:
        raise ValueError("Unsupported public EBI adapter")
    client = _http_client()
    try:
        with anyio.fail_after(CALL_TIMEOUT_SECONDS):
            yield _EBIJSONSession(client, service)
    except (httpx.TimeoutException, TimeoutError) as exc:
        raise TimeoutError(f"{service} query exceeded its whole-call deadline; retry later") from exc
    except httpx.HTTPStatusError as exc:
        raise RuntimeError(f"{service} request failed with HTTP {exc.response.status_code}") from exc
    except httpx.HTTPError as exc:
        raise RuntimeError(f"{service} upstream is unavailable") from exc
    finally:
        with anyio.move_on_after(1, shield=True):
            await client.aclose()
