"""Public retrieval must not require disclosure of a personal email address."""
import json
import os
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch
from urllib.parse import parse_qs

from mcp_pubmed import server
from pubmed_fetch.fetch import PubMedFetcher
from pubmed_search.client import PubMedSearch


class PubMedOptionalContactTest(unittest.TestCase):
    def setUp(self):
        # Keep unrelated process configuration, but isolate both contact inputs.
        contact_env = patch.dict(os.environ)
        contact_env.start()
        self.addCleanup(contact_env.stop)
        os.environ.pop("OPERON_CONTACT_EMAIL", None)
        os.environ.pop("NCBI_EMAIL", None)

    def test_factories_allow_no_contact_without_inventing_one(self):
        for factory in (server._search, server._fetcher, server._citmatch_search):
            with self.subTest(factory=factory):
                factory.cache_clear()
                client = factory()
                try:
                    self.assertIsNone(client.email)
                finally:
                    client.close()
                    factory.cache_clear()

    def test_search_omits_missing_contact_on_real_http(self):
        check_public_http(self, "search")

    def test_fetch_omits_missing_contact_on_real_http(self):
        check_public_http(self, "fetch")

    def test_explicit_contact_is_preserved_and_invalid_value_rejected(self):
        os.environ["OPERON_CONTACT_EMAIL"] = "consented@example.invalid"
        self.assertEqual(server._contact_email(), "consented@example.invalid")
        os.environ["NCBI_EMAIL"] = "operator@example.invalid"
        self.assertEqual(server._contact_email(), "operator@example.invalid")
        with PubMedSearch(email="operator@example.invalid") as search:
            self.assertEqual(search._ident({})["email"], "operator@example.invalid")
        with PubMedFetcher(email="operator@example.invalid") as fetch:
            self.assertEqual(fetch._common_params()["email"], "operator@example.invalid")
        for client in (PubMedSearch, PubMedFetcher):
            with self.subTest(client=client), self.assertRaises(ValueError):
                client(email="not-an-address")


def check_public_http(test, client_kind):
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            payload = self.rfile.read(int(self.headers["Content-Length"]))
            received.append((parse_qs(payload.decode(), keep_blank_values=True), self.headers["User-Agent"]))
            data = json.dumps({"esearchresult": {"count": "1", "idlist": ["123"]}}).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *_):
            pass

    http = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=http.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{http.server_port}"
    try:
        if client_kind == "search":
            with PubMedSearch(email=None, sleep_s=0, max_retries=0) as client:
                test.assertEqual(client._request(url, {"term": "fixture"}).status_code, 200)
        else:
            with patch("pubmed_fetch.fetch.EUTILS_BASE", url):
                with PubMedFetcher(email=None, sleep_s=0, max_retries=0) as client:
                    test.assertEqual(client._request("esearch.fcgi", {"term": "fixture"}).status_code, 200)
    finally:
        http.shutdown()
        http.server_close()
        thread.join(timeout=3)
    test.assertFalse(thread.is_alive())
    test.assertEqual(len(received), 1)
    params, agent = received[0]
    test.assertNotIn("email", params)
    test.assertNotIn("api_key", params)
    test.assertTrue(params["tool"])
    test.assertNotIn("mailto:None", agent)


if __name__ == "__main__":
    unittest.main()
