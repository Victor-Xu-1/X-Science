"""Exact Chemistry lookup contracts; isolated data, never a scientific acceptance task."""
from __future__ import annotations

from pathlib import Path
import asyncio
import json
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

import anyio
import httpx
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

LIB = Path(__file__).resolve().parents[1] / "lib"
sys.path.insert(0, str(LIB))

from mcp_chemistry import server as chemistry
from mcp_chemistry import lipidmaps

ARGS = {"id_type": "lm_id", "identifier": "LMFA01010001"}
ROW = {
    "lm_id": "LMFA01010001", "pubchem_cid": "985", "name": "Palmitic acid",
    "core": "Fatty Acyls [FA]", "main_class": "Fatty Acids and Conjugates [FA01]",
    "formula": "C16H32O2", "exactmass": "256.240231", "smiles": "OC(CCCCCCCCCCCCCCC)=O",
}


class _Chunks(httpx.AsyncByteStream):
    def __init__(self, chunks: list[bytes], *, stall: bool = False) -> None:
        self.chunks = chunks
        self.stall = stall
        self.started = asyncio.Event()
        self.closed = False

    async def __aiter__(self):
        self.started.set()
        if self.stall:
            await anyio.sleep_forever()
        for chunk in self.chunks:
            yield chunk

    async def aclose(self):
        self.closed = True


class LipidMapsRetrievalContractTests(unittest.IsolatedAsyncioTestCase):
    async def _call(self, body=ROW, arguments=None, *, raw=None, status=200, headers=None, stream=None):
        requests = []
        payload = json.dumps(body).encode() if raw is None else raw
        stream = stream or _Chunks([payload])

        async def handle(request):
            requests.append(request)
            return httpx.Response(status, stream=stream, headers=headers)

        client = httpx.AsyncClient(transport=httpx.MockTransport(handle), follow_redirects=False, trust_env=False)
        with patch.object(lipidmaps, "_http_client", return_value=client):
            result = await lipidmaps.get_compound(**(arguments or ARGS))
        self.assertTrue(client.is_closed)
        self.assertTrue(stream.closed)
        self.assertEqual(len(requests), 1)
        self.assertEqual(str(requests[0].url), result["source_url"])
        self.assertNotIn("authorization", requests[0].headers)
        self.assertNotIn("cookie", requests[0].headers)
        return result

    async def test_invalid_or_broad_identifiers_never_construct_transport(self):
        cases = [
            ("name", "palmitic acid"), ([], "985"), ("lm_id", "LMFA"),
            ("lm_id", "LMPR010453"), ("lm_id", "LMXX01010001"),
            ("lm_id", " LMFA01010001"), ("lm_id", "lmfa01010001"),
            ("lm_id", "LMFA01010001/all"), ("lm_id", "LMFA01010001,LMST01010001"),
            ("pubchem_cid", 985), ("pubchem_cid", True), ("pubchem_cid", None),
            ("pubchem_cid", "0"), ("pubchem_cid", "0985"), ("pubchem_cid", "-1"),
            ("pubchem_cid", "9.85"), ("pubchem_cid", "1e3"),
            ("pubchem_cid", "9999999999999999"), ("pubchem_cid", "https://example.org"),
        ]
        with patch.object(lipidmaps, "_http_client") as factory:
            for id_type, identifier in cases:
                with self.subTest(id_type=id_type, identifier=identifier):
                    with self.assertRaises(ValueError):
                        await lipidmaps.get_compound(id_type, identifier)
            factory.assert_not_called()

    async def test_exact_lmid_and_cid_preserve_native_fields_and_attribution(self):
        for arguments in (ARGS, {"id_type": "pubchem_cid", "identifier": "985"}):
            with self.subTest(arguments=arguments):
                result = await self._call(arguments=arguments)
                self.assertEqual(result["records"][0], {
                    **ROW, "record_url": "https://www.lipidmaps.org/databases/lmsd/LMFA01010001",
                })
                self.assertEqual(result["total_matches"], 1)
                self.assertFalse(result["records_truncated"])
                self.assertEqual(result["license"], "CC BY 4.0")
                self.assertEqual(result["license_url"], "https://www.lipidmaps.org/terms-of-use")
                self.assertEqual(result["identifier"], arguments["identifier"])

    async def test_fourth_level_alphanumeric_and_generic_lmids_are_not_rewritten(self):
        for lmid in ("LMPR0104530001", "LMSP0501AA01", "LMGP01010000"):
            with self.subTest(lmid=lmid):
                result = await self._call(
                    {**ROW, "lm_id": lmid, "class_level4": None},
                    {"id_type": "lm_id", "identifier": lmid},
                )
                self.assertEqual(result["records"][0]["lm_id"], lmid)
                self.assertIsNone(result["records"][0]["class_level4"])

    async def test_null_absence_and_unknown_fields_are_distinct(self):
        row = {"lm_id": ARGS["identifier"], "core": "FA", "main_class": "FA01", "name": None,
               "synonyms": ["not a supported output field"], "unexpected": {"secret": True}}
        result = await self._call(row)
        self.assertEqual(result["records"][0], {
            "lm_id": ARGS["identifier"], "core": "FA", "main_class": "FA01", "name": None,
            "record_url": "https://www.lipidmaps.org/databases/lmsd/LMFA01010001",
        })

    async def test_only_real_empty_array_means_no_match(self):
        result = await self._call([])
        self.assertEqual(result["records"], [])
        self.assertEqual(result["total_matches"], 0)
        self.assertFalse(result["records_truncated"])

    async def test_multiple_matches_keep_real_total_and_disclosed_display_cap(self):
        rows = {f"Row{i}": {**ROW, "lm_id": f"LMFA0101{i:04}"} for i in range(1, 7)}
        result = await self._call(rows, {"id_type": "pubchem_cid", "identifier": "985"})
        self.assertEqual(result["total_matches"], 6)
        self.assertEqual(len(result["records"]), 5)
        self.assertTrue(result["records_truncated"])

    async def test_identity_checks_include_rows_beyond_display_cap(self):
        rows = {f"Row{i}": {**ROW, "pubchem_cid": "5997" if i == 6 else "985"} for i in range(1, 7)}
        with self.assertRaisesRegex(ValueError, "inconsistent"):
            await self._call(rows, {"id_type": "pubchem_cid", "identifier": "985"})
        with self.assertRaisesRegex(ValueError, "inconsistent"):
            await self._call({**ROW, "lm_id": "LMST01010001"})

    async def test_malformed_envelopes_and_fields_are_not_empty_success(self):
        invalid = [None, {}, "upstream error", {"error": "invalid query"}, [ROW],
                   {"Row1": ROW, "error": "partial response"}, {"Row0": ROW}, {"Row1": None},
                   {**ROW, "lm_id": "bad-id"}, {**ROW, "core": None}, {**ROW, "core": ""},
                   {**ROW, "main_class": "A" * 201}, {**ROW, "exactmass": 256.24},
                   {**ROW, "exactmass": "1e3"}, {**ROW, "pubchem_cid": "0985"},
                   {**ROW, "name": ["not text"]}, {**ROW, "smiles": "C" * 2001}]
        for body in invalid:
            with self.subTest(body=body):
                with self.assertRaises(ValueError):
                    await self._call(body)

    async def test_http_failure_or_redirect_never_becomes_no_match(self):
        for status in (302, 404, 429, 500):
            with self.subTest(status=status):
                with self.assertRaisesRegex(RuntimeError, f"HTTP {status}"):
                    await self._call(status=status, headers={"location": "https://example.org/private"})

    async def test_invalid_json_duplicate_fields_and_byte_overrun_fail(self):
        for raw in (b"<html>unavailable</html>", b"{\"Row1\":{},\"Row1\":{}}",
                    json.dumps({**ROW, "unexpected": float("nan")}).encode(), b" " * 100_001):
            with self.subTest(size=len(raw)):
                with self.assertRaises(ValueError):
                    await self._call(raw=raw)
        with self.assertRaisesRegex(ValueError, "too many"):
            await self._call({f"Row{i}": ROW for i in range(1, 102)})

    async def test_unexpected_compression_is_rejected_before_expansion(self):
        with self.assertRaisesRegex(ValueError, "content encoding"):
            await self._call(headers={"content-encoding": "gzip"}, raw=b"unread compressed bytes")

    async def test_whole_call_deadline_closes_the_live_stream(self):
        stream = _Chunks([], stall=True)
        client = httpx.AsyncClient(transport=httpx.MockTransport(
            lambda request: httpx.Response(200, stream=stream)), trust_env=False)
        with patch.object(lipidmaps, "_http_client", return_value=client), patch.object(
            lipidmaps, "CALL_TIMEOUT_SECONDS", 0.01
        ):
            with self.assertRaisesRegex(TimeoutError, "whole-call deadline"):
                await lipidmaps.get_compound(**ARGS)
        self.assertTrue(client.is_closed)
        self.assertTrue(stream.closed)

    async def test_caller_cancellation_is_not_an_empty_result_or_retry(self):
        stream = _Chunks([], stall=True)
        client = httpx.AsyncClient(transport=httpx.MockTransport(
            lambda request: httpx.Response(200, stream=stream)), trust_env=False)
        with patch.object(lipidmaps, "_http_client", return_value=client):
            task = asyncio.create_task(lipidmaps.get_compound(**ARGS))
            with anyio.fail_after(1):
                await stream.started.wait()
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await task
        self.assertTrue(client.is_closed)
        self.assertTrue(stream.closed)


class LipidMapsFacadeContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_exact_lookup_uses_existing_chemistry_facade(self) -> None:
        tools = {tool.name: tool for tool in await chemistry.mcp.list_tools()}
        self.assertIn("pubchem_search_compounds", tools)
        self.assertIn("lipidmaps_get_compound", sorted(tools))
        tool = tools["lipidmaps_get_compound"]
        self.assertTrue(tool.annotations.readOnlyHint)
        self.assertEqual(tool.inputSchema["properties"]["identifier"]["examples"], ["LMFA01010001"])
        self.assertEqual(set(tool.inputSchema["required"]), {"id_type", "identifier"})
        self.assertEqual(
            tool.inputSchema["properties"]["id_type"]["enum"],
            ["lm_id", "pubchem_cid"],
        )

    async def test_aggregate_roster_contains_exact_actual_methods(self):
        roster = json.loads((LIB / "mcp_bio/domains.json").read_text())["chemistry"]
        methods = [tool.name for tool in await chemistry.mcp.list_tools()]
        self.assertEqual(sorted(methods), sorted(roster))
        self.assertEqual(methods.count("lipidmaps_get_compound"), 1)

    async def test_packaged_real_stdio_and_controlled_http_call(self):
        received: list[str] = []

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(self.path)
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps(ROW).encode())

            def log_message(self, *_args):
                pass

        upstream = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        worker = threading.Thread(target=upstream.serve_forever, daemon=True)
        worker.start()
        wrapper = '''import sys
import httpx
sys.path.insert(0, sys.argv[1])
from mcp_chemistry import server, lipidmaps
class Mapped(httpx.AsyncBaseTransport):
    def __init__(self):
        self.transport = httpx.AsyncHTTPTransport()
    async def handle_async_request(self, request):
        assert request.url.scheme == "https" and request.url.host == "www.lipidmaps.org"
        mapped = httpx.Request(request.method, sys.argv[2] + request.url.raw_path.decode(), headers=request.headers)
        return await self.transport.handle_async_request(mapped)
    async def aclose(self):
        await self.transport.aclose()
lipidmaps._http_client = lambda: httpx.AsyncClient(transport=Mapped(), trust_env=False, follow_redirects=False)
server.main()
'''
        params = StdioServerParameters(
            command=sys.executable,
            args=["-B", "-c", wrapper, str(LIB), f"http://127.0.0.1:{upstream.server_port}"],
            env={"PYTHONDONTWRITEBYTECODE": "1", "PYTHONPATH": str(LIB)},
        )
        try:
            with anyio.fail_after(15):
                async with stdio_client(params) as (read, write):
                    async with ClientSession(read, write) as session:
                        await session.initialize()
                        tools = (await session.list_tools()).tools
                        tool = next(t for t in tools if t.name == "lipidmaps_get_compound")
                        self.assertTrue(tool.annotations.readOnlyHint)
                        response = await session.call_tool("lipidmaps_get_compound", ARGS)
                        self.assertFalse(response.isError)
                        result = response.structuredContent
                        if result is None:
                            result = json.loads("".join(block.text for block in response.content if block.type == "text"))
                        self.assertEqual(result["records"][0]["lm_id"], ROW["lm_id"])
                        self.assertEqual(result["license"], "CC BY 4.0")
                        self.assertEqual(result["total_matches"], 1)
                        rejected = await session.call_tool("lipidmaps_get_compound", {**ARGS, "identifier": "LMFA"})
                        self.assertTrue(rejected.isError)
                        rejected = await session.call_tool("lipidmaps_get_compound", {"id_type": "pubchem_cid", "identifier": 985})
                        self.assertTrue(rejected.isError)
                        self.assertEqual(received, ["/rest/compound/lm_id/LMFA01010001/all"])
        finally:
            upstream.shutdown()
            upstream.server_close()
            worker.join(timeout=2)
            self.assertFalse(worker.is_alive())


if __name__ == "__main__":
    unittest.main()
