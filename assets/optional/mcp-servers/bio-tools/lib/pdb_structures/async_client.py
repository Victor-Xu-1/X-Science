"""One bounded native-async RCSB call, not a scientific-task duration limit."""
from __future__ import annotations

from contextlib import asynccontextmanager
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import math
import time
from urllib.parse import quote

import anyio
import httpx

from .client import SEARCH_URL, DATA_BASE_URL
from .search_validation import decode_json, object_value

STRUCTURE_TIMEOUT_SECONDS = 25.0
# Adapted to the current 60-second outer MCP call. The reference defaults to 120s.
SEQUENCE_TIMEOUT_SECONDS = 50.0
STRUCTURE_MAX_RESPONSE_BYTES = 500_000
SEQUENCE_MAX_RESPONSE_BYTES = 64 * 1024 * 1024
_RETRY_STATUS = frozenset({429, 500, 502, 503, 504})
_BACKOFF_SECONDS = 0.4


def _http_client() -> httpx.AsyncClient:
    # The operator, not a tool/model argument, owns proxy, CA and TLS posture.
    return httpx.AsyncClient(
        follow_redirects=False, trust_env=True, timeout=httpx.Timeout(30.0),
        headers={"Accept": "application/json", "Accept-Encoding": "identity",
                 "User-Agent": "X-Science/pdb-search"},
    )


def _retry_delay(value: str | None, fallback: float) -> float:
    if value is None:
        return fallback
    try:
        result = float(value)
        if math.isfinite(result) and result >= 0:
            return result
    except ValueError:
        pass
    try:
        date = parsedate_to_datetime(value)
        if date.tzinfo is None:
            date = date.replace(tzinfo=timezone.utc)
        return max(0.0, (date - datetime.now(timezone.utc)).total_seconds())
    except (ValueError, TypeError, OverflowError):
        return fallback


class _RCSBSession:
    def __init__(self, client: httpx.AsyncClient, *, kind: str, timeout: float,
                 byte_limit: int, request_limit: int, retries: int):
        self.client = client
        self.kind = kind
        self.deadline = time.monotonic() + timeout
        self.byte_limit = byte_limit
        self.request_limit = request_limit
        self.retries_left = retries
        self.retries_used = 0
        self.requests = 0

    def require_deadline(self) -> None:
        if time.monotonic() >= self.deadline:
            raise TimeoutError("RCSB search exceeded its whole-call deadline; retry later")

    async def post_search(self, payload: dict) -> object | None:
        return await self._request("POST", SEARCH_URL, payload, search=True)

    async def get_polymer_entity(self, pdb_id: str, entity_id: str) -> object | None:
        url = f"{DATA_BASE_URL}/polymer_entity/{quote(pdb_id, safe='')}/{quote(entity_id, safe='')}"
        return await self._request("GET", url, None, search=False)

    async def _request(self, method: str, url: str, payload: dict | None, *,
                       search: bool) -> object | None:
        while True:
            await anyio.lowlevel.checkpoint()
            self.require_deadline()
            if self.requests >= self.request_limit:
                raise RuntimeError("RCSB request count exceeds the whole-call budget")
            self.requests += 1
            delay = _BACKOFF_SECONDS * 2 ** self.retries_used
            reason: Exception | None = None
            try:
                async with self.client.stream(method, url, json=payload) as response:
                    status = response.status_code
                    if status in _RETRY_STATUS:
                        reason = RuntimeError(f"RCSB request failed with HTTP {status}")
                        delay = _retry_delay(response.headers.get("Retry-After"), delay)
                    elif status == 204 and search or status == 404 and not search:
                        return None
                    elif status != 200:
                        raise RuntimeError(f"RCSB request failed with HTTP {status}")
                    else:
                        if response.headers.get("Content-Encoding", "identity").strip().lower() != "identity":
                            raise ValueError("RCSB returned an unsupported content encoding")
                        body = bytearray()
                        async for chunk in response.aiter_raw():
                            self.require_deadline()
                            if len(body) + len(chunk) > self.byte_limit:
                                raise ValueError("RCSB response exceeds the byte budget")
                            body.extend(chunk)
                        self.require_deadline()
                        # None is reserved for real search-204 / metadata-404.
                        # A successful HTTP body containing JSON null is corrupt,
                        # not evidence of an absent match or entity.
                        decoded = object_value(decode_json(body), "JSON response")
                        self.require_deadline()
                        return decoded
            except httpx.TransportError:
                reason = RuntimeError("RCSB upstream is unavailable")
            if not self.retries_left or delay >= self.deadline - time.monotonic():
                raise reason
            self.retries_left -= 1
            self.retries_used += 1
            await anyio.sleep(delay)


@asynccontextmanager
async def rcsb_json_session(kind: str, *, max_candidates: int = 100, max_rows: int = 10):
    if kind == "structure":
        timeout, byte_limit, request_limit, retries = (
            STRUCTURE_TIMEOUT_SECONDS, STRUCTURE_MAX_RESPONSE_BYTES, 1, 0)
    elif kind == "sequence":
        timeout, byte_limit, request_limit, retries = (
            SEQUENCE_TIMEOUT_SECONDS, SEQUENCE_MAX_RESPONSE_BYTES,
            (max_candidates + 99) // 100 + max_rows + 2, 2)
    else:
        raise ValueError("Unknown RCSB search operation")
    client = _http_client()
    session = _RCSBSession(client, kind=kind, timeout=timeout, byte_limit=byte_limit,
                           request_limit=request_limit, retries=retries)
    try:
        with anyio.fail_after(timeout):
            yield session
            session.require_deadline()
    except (TimeoutError, httpx.TimeoutException) as exc:
        raise TimeoutError(f"RCSB {kind} search exceeded its whole-call deadline; retry later") from exc
    finally:
        with anyio.move_on_after(1, shield=True):
            await client.aclose()
