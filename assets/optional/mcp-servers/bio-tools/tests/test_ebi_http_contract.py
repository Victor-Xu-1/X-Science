"""Only the shared public EBI read transport; no scientific or live-provider task."""
from pathlib import Path
import asyncio
import json
import sys
import unittest
from unittest.mock import patch

import anyio
import httpx

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "lib"))
from mcp_servers_common import ebi_http

PDBe = "https://www.ebi.ac.uk/pdbe/api/pdb/entry/summary/1cbs"
UniChem = "https://www.ebi.ac.uk/unichem/api/v1/compounds"


class _Chunks(httpx.AsyncByteStream):
    def __init__(self, chunks=(), *, stall=False):
        self.chunks, self.stall, self.closed = chunks, stall, False
        self.started = asyncio.Event()

    async def __aiter__(self):
        self.started.set()
        for chunk in self.chunks:
            yield chunk
        if self.stall:
            await anyio.sleep_forever()

    async def aclose(self):
        self.closed = True


class EBIHTTPContracts(unittest.IsolatedAsyncioTestCase):
    def client(self, handler):
        return httpx.AsyncClient(transport=httpx.MockTransport(handler), trust_env=False, follow_redirects=False)

    async def test_exact_get_and_post_preserve_wire_body_and_close_client(self):
        for service, method, url, payload in (("PDBe", "GET", PDBe, None),
                                             ("UniChem", "POST", UniChem, {"compound": "CHEMBL1", "type": "sourceID", "sourceID": 1})):
            calls = []
            def handler(request):
                calls.append(request)
                return httpx.Response(200, stream=_Chunks([b'{"source":"controlled"}']))
            client = self.client(handler)
            with patch.object(ebi_http, "_http_client", return_value=client):
                async with ebi_http.ebi_json_session(service) as session:
                    self.assertEqual(await session.request(method, url, json=payload), (200, {"source": "controlled"}))
            self.assertTrue(client.is_closed)
            self.assertEqual(len(calls), 1)
            self.assertEqual(str(calls[0].url), url)
            self.assertEqual(calls[0].method, method)
            if payload is not None:
                self.assertEqual(json.loads(calls[0].content), payload)

    async def test_provider_endpoint_cannot_be_replaced_or_redirected(self):
        calls = []
        client = self.client(lambda request: calls.append(request))
        with patch.object(ebi_http, "_http_client", return_value=client):
            async with ebi_http.ebi_json_session("PDBe") as session:
                for url in (PDBe.replace("www.ebi.ac.uk", "foreign.invalid"), PDBe.replace("https:", "http:"),
                            PDBe + "?token=ignored", PDBe + "#fragment", "\n" + PDBe, UniChem):
                    with self.subTest(url=url), self.assertRaisesRegex(ValueError, "endpoint"):
                        await session.request("GET", url)
        self.assertEqual(calls, [])
        self.assertTrue(client.is_closed)

    async def test_only_pdbe_may_return_explicit_404_body_to_its_parser(self):
        client = self.client(lambda request: httpx.Response(404, stream=_Chunks([b'{"message":"known absence"}'])))
        with patch.object(ebi_http, "_http_client", return_value=client):
            async with ebi_http.ebi_json_session("PDBe") as session:
                self.assertEqual(await session.request("GET", PDBe, allow_not_found=True), (404, {"message": "known absence"}))
        client = self.client(lambda request: httpx.Response(404, stream=_Chunks([b'{}'])))
        with patch.object(ebi_http, "_http_client", return_value=client), self.assertRaisesRegex(RuntimeError, "HTTP 404"):
            async with ebi_http.ebi_json_session("UniChem") as session:
                await session.request("POST", UniChem, json={})
        self.assertTrue(client.is_closed)

    async def test_http_errors_and_redirects_are_not_empty_success(self):
        for status in (302, 429, 500):
            with self.subTest(status=status):
                client = self.client(lambda request: httpx.Response(status, headers={"location": "https://foreign.invalid"}, stream=_Chunks([b'{}'])))
                with patch.object(ebi_http, "_http_client", return_value=client), self.assertRaisesRegex(RuntimeError, f"HTTP {status}"):
                    async with ebi_http.ebi_json_session("PDBe") as session:
                        await session.request("GET", PDBe)
                self.assertTrue(client.is_closed)

    async def test_duplicate_nonfinite_invalid_utf8_and_deep_json_are_errors(self):
        for raw in (b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":1e999}', b'\xff', b'<html>error</html>', b'[' * 1200 + b'0' + b']' * 1200):
            with self.subTest(raw_prefix=raw[:12]):
                client = self.client(lambda request: httpx.Response(200, stream=_Chunks([raw])))
                with patch.object(ebi_http, "_http_client", return_value=client), self.assertRaisesRegex(ValueError, "invalid JSON"):
                    async with ebi_http.ebi_json_session("PDBe") as session:
                        await session.request("GET", PDBe)
                self.assertTrue(client.is_closed)

    async def test_depth_preflight_ignores_json_punctuation_in_escaped_strings(self):
        expected = {"text": 'braces { [ ] } and escaped quote " slash \\ ' * 70}
        client = self.client(lambda request: httpx.Response(200, stream=_Chunks([json.dumps(expected).encode()])))
        with patch.object(ebi_http, "_http_client", return_value=client):
            async with ebi_http.ebi_json_session("PDBe") as session:
                self.assertEqual(await session.request("GET", PDBe), (200, expected))

    async def test_streaming_byte_and_encoding_budgets_do_not_expand_untrusted_content(self):
        for service, url, method, limit in (("PDBe", PDBe, "GET", 100_000), ("UniChem", UniChem, "POST", 1_000_000)):
            stream = _Chunks([b' ' * limit, b'x'])
            client = self.client(lambda request: httpx.Response(200, stream=stream))
            with patch.object(ebi_http, "_http_client", return_value=client), self.assertRaisesRegex(ValueError, "byte budget"):
                async with ebi_http.ebi_json_session(service) as session:
                    await session.request(method, url)
            self.assertTrue(stream.closed)
        stream = _Chunks([b'not even inflated'])
        client = self.client(lambda request: httpx.Response(200, headers={"Content-Encoding": "gzip"}, stream=stream))
        with patch.object(ebi_http, "_http_client", return_value=client), self.assertRaisesRegex(ValueError, "content encoding"):
            async with ebi_http.ebi_json_session("PDBe") as session:
                await session.request("GET", PDBe)
        self.assertTrue(stream.closed)

    async def test_request_count_is_bounded_without_silent_retry(self):
        calls = []
        def handler(request):
            calls.append(request)
            return httpx.Response(200, stream=_Chunks([b'{}']))
        client = self.client(handler)
        with patch.object(ebi_http, "_http_client", return_value=client):
            async with ebi_http.ebi_json_session("PDBe") as session:
                for _ in range(3):
                    await session.request("GET", PDBe)
                with self.assertRaisesRegex(RuntimeError, "request count"):
                    await session.request("GET", PDBe)
        self.assertEqual(len(calls), 3)

    async def test_whole_call_deadline_closes_live_response(self):
        stream = _Chunks(stall=True)
        client = self.client(lambda request: httpx.Response(200, stream=stream))
        with patch.object(ebi_http, "_http_client", return_value=client), patch.object(ebi_http, "CALL_TIMEOUT_SECONDS", 0.01):
            with self.assertRaisesRegex(TimeoutError, "whole-call deadline"):
                async with ebi_http.ebi_json_session("PDBe") as session:
                    await session.request("GET", PDBe)
        self.assertTrue(stream.closed)
        self.assertTrue(client.is_closed)

    async def test_caller_cancellation_propagates_and_reaps_only_its_socket(self):
        stream = _Chunks(stall=True)
        client = self.client(lambda request: httpx.Response(200, stream=stream))
        async def execute():
            async with ebi_http.ebi_json_session("PDBe") as session:
                await session.request("GET", PDBe)
        with patch.object(ebi_http, "_http_client", return_value=client):
            task = asyncio.create_task(execute())
            with anyio.fail_after(1):
                await stream.started.wait()
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await task
        self.assertTrue(stream.closed)
        self.assertTrue(client.is_closed)


if __name__ == "__main__":
    unittest.main()
