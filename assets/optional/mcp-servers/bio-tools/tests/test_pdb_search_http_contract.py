"""Native async RCSB boundary: bytes, retry scope, deadline, policy and cleanup."""
from __future__ import annotations

import asyncio
import importlib
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

import anyio
import httpx

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "lib"))


class Stream(httpx.AsyncByteStream):
    def __init__(self, chunks, *, block=False):
        self.chunks, self.block, self.closed = chunks, block, False
        self.entered = asyncio.Event()

    async def __aiter__(self):
        self.entered.set()
        if self.block:
            await anyio.sleep(30)
        for value in self.chunks:
            await anyio.lowlevel.checkpoint()
            yield value

    async def aclose(self):
        self.closed = True


def response(data, status=200, headers=None):
    return httpx.Response(status, stream=Stream([json.dumps(data).encode()]), headers=headers)


class PDBHTTPContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_factory_keeps_operator_trust_and_refuses_redirects(self):
        transport = importlib.import_module("pdb_structures.async_client")
        with patch.object(transport.httpx, "AsyncClient") as factory:
            transport._http_client()
        options = factory.call_args.kwargs
        self.assertIs(options["trust_env"], True)
        self.assertIs(options["follow_redirects"], False)
        self.assertNotIn("verify", options)
        self.assertEqual(options["headers"]["Accept-Encoding"], "identity")
        self.assertEqual(transport.STRUCTURE_TIMEOUT_SECONDS, 25)
        self.assertEqual(transport.SEQUENCE_TIMEOUT_SECONDS, 50)
        self.assertEqual(transport.STRUCTURE_MAX_RESPONSE_BYTES, 500000)
        self.assertEqual(transport.SEQUENCE_MAX_RESPONSE_BYTES, 64 * 1024 * 1024)

    async def test_retry_allowance_is_shared_not_repeated_per_request(self):
        transport = importlib.import_module("pdb_structures.async_client")
        requests, statuses = [], iter((503, 200, 429, 200, 503))
        def respond(request):
            requests.append(request)
            return response({"ok": True}, next(statuses))
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client), patch.object(transport, "_BACKOFF_SECONDS", 0):
            with self.assertRaisesRegex(RuntimeError, "HTTP 503"):
                async with transport.rcsb_json_session("sequence", max_candidates=101, max_rows=1) as session:
                    await session.post_search({})
                    await session.post_search({})
                    await session.get_polymer_entity("1ABC", "1")
        self.assertEqual(len(requests), 5)
        self.assertTrue(client.is_closed)

    async def test_request_budget_includes_search_metadata_and_only_two_retry_attempts(self):
        transport = importlib.import_module("pdb_structures.async_client")
        requests = []
        def respond(request):
            requests.append(request)
            return response({})
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            with self.assertRaisesRegex(RuntimeError, "request count"):
                async with transport.rcsb_json_session("sequence", max_candidates=1, max_rows=1) as session:
                    for _ in range(5):
                        await session.post_search({})
        self.assertEqual(len(requests), 4)
        self.assertTrue(client.is_closed)

    async def test_long_retry_after_is_not_shortened_or_hidden_by_a_new_request(self):
        transport = importlib.import_module("pdb_structures.async_client")
        requests = []
        def respond(request):
            requests.append(request)
            return response({}, 429, {"Retry-After": "120"})
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            with self.assertRaisesRegex(RuntimeError, "HTTP 429"):
                async with transport.rcsb_json_session("sequence") as session:
                    await session.post_search({})
        self.assertEqual(len(requests), 1)
        self.assertTrue(client.is_closed)

    async def test_transport_errors_have_no_body_diagnostics_and_structure_never_retries(self):
        transport = importlib.import_module("pdb_structures.async_client")
        for kind, attempts in (("structure", 1), ("sequence", 3)):
            with self.subTest(kind=kind):
                requests = []
                def respond(request):
                    requests.append(request)
                    raise httpx.ConnectError("private credential or query diagnostic", request=request)
                client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client), patch.object(transport, "_BACKOFF_SECONDS", 0):
                    with self.assertRaisesRegex(RuntimeError, "upstream is unavailable") as raised:
                        async with transport.rcsb_json_session(kind) as session:
                            await session.post_search({})
                self.assertNotIn("private", str(raised.exception))
                self.assertEqual(len(requests), attempts)
                self.assertTrue(client.is_closed)

    async def test_empty_and_not_found_contracts_are_method_specific(self):
        transport = importlib.import_module("pdb_structures.async_client")
        for search, status, absent in ((True, 204, True), (False, 404, True),
                                      (True, 404, False), (False, 204, False)):
            with self.subTest(search=search, status=status):
                client = httpx.AsyncClient(transport=httpx.MockTransport(
                    lambda request: response({}, status)), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client):
                    async with transport.rcsb_json_session("sequence") as session:
                        if absent:
                            result = await (session.post_search({}) if search else session.get_polymer_entity("1ABC", "1"))
                            self.assertIsNone(result)
                        else:
                            with self.assertRaisesRegex(RuntimeError, f"HTTP {status}"):
                                await (session.post_search({}) if search else session.get_polymer_entity("1ABC", "1"))
                self.assertTrue(client.is_closed)

    async def test_redirect_is_an_error_and_does_not_contact_location(self):
        transport = importlib.import_module("pdb_structures.async_client")
        requests = []
        def respond(request):
            requests.append(request)
            return response({}, 302, {"Location": "https://127.0.0.1/private"})
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False, follow_redirects=False)
        with patch.object(transport, "_http_client", return_value=client):
            with self.assertRaisesRegex(RuntimeError, "HTTP 302"):
                async with transport.rcsb_json_session("structure") as session:
                    await session.post_search({})
        self.assertEqual(len(requests), 1)
        self.assertEqual(requests[0].url.host, "search.rcsb.org")
        self.assertTrue(client.is_closed)

    async def test_strict_json_rejects_invalid_encoding_duplicates_numbers_and_depth(self):
        transport = importlib.import_module("pdb_structures.async_client")
        malformed = (b"", b"<html>maintenance</html>", b"\xff", b'{"x":1,"x":2}',
                     b'{"x":NaN}', b'{"x":Infinity}', b'{"x":1e400}',
                     b"[" * 65 + b"0" + b"]" * 65)
        for body in malformed:
            with self.subTest(body=body[:40]):
                stream = Stream([body])
                client = httpx.AsyncClient(transport=httpx.MockTransport(
                    lambda request: httpx.Response(200, stream=stream)), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client):
                    with self.assertRaisesRegex(ValueError, "invalid JSON"):
                        async with transport.rcsb_json_session("structure") as session:
                            await session.post_search({})
                self.assertTrue(stream.closed)
                self.assertTrue(client.is_closed)

    async def test_json_depth_scanner_does_not_count_quoted_or_escaped_delimiters(self):
        transport = importlib.import_module("pdb_structures.async_client")
        value = {"text": "[" * 80 + '\\"' + "}" * 80, "nested": {"x": [1]}}
        client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: response(value)), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            async with transport.rcsb_json_session("structure") as session:
                self.assertEqual(await session.post_search({}), value)
        self.assertTrue(client.is_closed)

    async def test_streamed_raw_byte_limit_and_content_encoding_are_enforced(self):
        transport = importlib.import_module("pdb_structures.async_client")
        for encoding, chunks, message in (("identity", [b" " * 8, b" " * 8], "byte budget"),
                                          ("gzip", [b"{}"], "content encoding")):
            with self.subTest(encoding=encoding):
                stream = Stream(chunks)
                client = httpx.AsyncClient(transport=httpx.MockTransport(
                    lambda request: httpx.Response(200, stream=stream, headers={"Content-Encoding": encoding})),
                    trust_env=False)
                with patch.object(transport, "_http_client", return_value=client), patch.object(
                        transport, "STRUCTURE_MAX_RESPONSE_BYTES", 10):
                    with self.assertRaisesRegex(ValueError, message):
                        async with transport.rcsb_json_session("structure") as session:
                            await session.post_search({})
                self.assertTrue(stream.closed)
                self.assertTrue(client.is_closed)

    async def test_whole_call_deadline_is_not_reset_by_later_reads(self):
        transport = importlib.import_module("pdb_structures.async_client")
        requests = []
        async def respond(request):
            requests.append(request)
            if len(requests) > 1:
                await anyio.sleep(30)
            return response({})
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client), patch.object(
                transport, "SEQUENCE_TIMEOUT_SECONDS", 0.03):
            with self.assertRaisesRegex(TimeoutError, "whole-call deadline"):
                async with transport.rcsb_json_session("sequence") as session:
                    await session.post_search({})
                    await session.get_polymer_entity("1ABC", "1")
        self.assertEqual(len(requests), 2)
        self.assertTrue(client.is_closed)

    async def test_cancellation_closes_stream_and_client_without_retry(self):
        transport = importlib.import_module("pdb_structures.async_client")
        stream, requests = Stream([], block=True), []
        def respond(request):
            requests.append(request)
            return httpx.Response(200, stream=stream)
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        async def run():
            async with transport.rcsb_json_session("sequence") as session:
                await session.post_search({})
        with patch.object(transport, "_http_client", return_value=client):
            task = asyncio.create_task(run())
            await asyncio.wait_for(stream.entered.wait(), 1)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
        self.assertEqual(len(requests), 1)
        self.assertTrue(stream.closed)
        self.assertTrue(client.is_closed)


if __name__ == "__main__":
    unittest.main()
