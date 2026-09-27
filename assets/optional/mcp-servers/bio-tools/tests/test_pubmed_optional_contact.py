"""Public retrieval must not require disclosure of a personal email address."""
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs

import pytest

from mcp_pubmed import server
from pubmed_fetch.fetch import PubMedFetcher
from pubmed_search.client import PubMedSearch


def test_pubmed_factories_allow_no_contact_without_inventing_one(monkeypatch):
    monkeypatch.delenv("OPERON_CONTACT_EMAIL", raising=False)
    monkeypatch.delenv("NCBI_EMAIL", raising=False)
    for factory in (server._search, server._fetcher, server._citmatch_search):
        factory.cache_clear()
        client = factory()
        try:
            assert client.email is None
        finally:
            client.close()
            factory.cache_clear()


@pytest.mark.parametrize("client_kind", ["search", "fetch"])
def test_public_clients_omit_missing_contact_on_real_http(monkeypatch, client_kind):
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
                assert client._request(url, {"term": "fixture"}).status_code == 200
        else:
            monkeypatch.setattr("pubmed_fetch.fetch.EUTILS_BASE", url)
            with PubMedFetcher(email=None, sleep_s=0, max_retries=0) as client:
                assert client._request("esearch.fcgi", {"term": "fixture"}).status_code == 200
    finally:
        http.shutdown()
        http.server_close()
        thread.join(timeout=3)
    assert len(received) == 1
    params, agent = received[0]
    assert "email" not in params
    assert "api_key" not in params
    assert params["tool"]
    assert "mailto:None" not in agent


def test_explicit_contact_is_preserved_and_invalid_value_rejected(monkeypatch):
    monkeypatch.setenv("OPERON_CONTACT_EMAIL", "consented@example.invalid")
    monkeypatch.delenv("NCBI_EMAIL", raising=False)
    assert server._contact_email() == "consented@example.invalid"
    monkeypatch.setenv("NCBI_EMAIL", "operator@example.invalid")
    assert server._contact_email() == "operator@example.invalid"
    with PubMedSearch(email="operator@example.invalid") as search:
        assert search._ident({})["email"] == "operator@example.invalid"
    with PubMedFetcher(email="operator@example.invalid") as fetch:
        assert fetch._common_params()["email"] == "operator@example.invalid"
    for client in (PubMedSearch, PubMedFetcher):
        with pytest.raises(ValueError):
            client(email="not-an-address")
