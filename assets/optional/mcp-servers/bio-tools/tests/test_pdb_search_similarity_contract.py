"""Direct deposited-chain similarity contracts, using real async HTTPX transport."""
from __future__ import annotations

import importlib
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

import httpx

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "lib"))

SEARCH_URL = "https://search.rcsb.org/rcsbsearch/v2/query"


def hit(identifier="1ABC.A", score=1.0, raw_score=0.99):
    return {"identifier": identifier, "score": score,
            "services": [{"service_type": "structure",
                          "nodes": [{"original_score": raw_score}]}]}


def response(data, status=200):
    return httpx.Response(status, stream=httpx.ByteStream(json.dumps(data).encode()),
                          headers={"Content-Type": "application/json"})


class PDBSimilarityContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_fixed_query_and_chain_score_contract(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.similarity")
        requests = []

        def respond(request):
            requests.append(request)
            return response({"result_type": "polymer_instance", "total_count": 9,
                             "result_set": [hit(), hit("2DEF.a", 0, 0)]})

        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_structure_similarity("1abc", "a", max_rows=2)
        self.assertTrue(client.is_closed)
        self.assertEqual(len(requests), 1)
        request = requests[0]
        self.assertEqual(str(request.url), SEARCH_URL)
        self.assertEqual(request.method, "POST")
        self.assertEqual(json.loads(request.content), {
            "query": {"type": "terminal", "service": "structure", "parameters": {
                "value": {"entry_id": "1ABC", "asym_id": "a"},
                "target_search_space": "polymer_entity_instance", "similarity_type": "global",
                "ptmscore_cutoff": 0.8, "number_of_candidates": 10000}},
            "return_type": "polymer_instance", "request_options": {
                "paginate": {"start": 0, "rows": 2},
                "results_content_type": ["experimental"], "results_verbosity": "verbose"}})
        self.assertEqual(result["query"], {"pdb_id": "1ABC", "label_asym_id": "a",
                         "similarity_type": "global", "ptmscore_cutoff": 0.8,
                         "number_of_candidates": 10000})
        self.assertEqual((result["total_count"], result["n_returned"], result["truncated"]), (9, 2, True))
        self.assertEqual(result["records"][1], {"identifier": "2DEF.a", "pdb_id": "2DEF",
                         "label_asym_id": "a", "relevance_score": 0, "raw_structure_score": 0})
        self.assertIn("not", result["score_note"].lower())

    async def test_invalid_arguments_do_not_open_transport(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.similarity")
        with patch.object(transport, "_http_client") as factory:
            for arguments in (("pdb_00001abc", "A", 10), ("0ABC", "A", 10),
                              ("1ABC", "A/B", 10), ("1ABC", "", 10),
                              ("1ABC", "A", True), ("1ABC", "A", 51)):
                with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                    await search.search_structure_similarity(arguments[0], arguments[1], max_rows=arguments[2])
        factory.assert_not_called()

    async def test_empty_complete_and_display_capped_results(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.similarity")
        cases = [
            (204, None, 1, (0, 0, False)),
            (200, {"result_type": "polymer_instance", "total_count": 2,
                   "result_set": [hit(), hit("2DEF.A")]}, 2, (2, 2, False)),
            (200, {"result_type": "polymer_instance", "total_count": 2,
                   "result_set": [hit(), hit("2DEF.A")]}, 1, (2, 1, True)),
        ]
        for status, body, maximum, expected in cases:
            with self.subTest(status=status, maximum=maximum):
                client = httpx.AsyncClient(transport=httpx.MockTransport(
                    lambda request: response(body, status)), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client):
                    result = await search.search_structure_similarity("1ABC", "A", max_rows=maximum)
                self.assertEqual((result["total_count"], result["n_returned"], result["truncated"]), expected)
                self.assertTrue(client.is_closed)

    async def test_all_response_rows_are_validated_before_display_slice(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.similarity")
        cases = [None, {}, {"result_type": "entry", "total_count": 0},
                 {"result_type": "polymer_instance", "total_count": True},
                 {"result_type": "polymer_instance", "total_count": 2, "result_set": []},
                 {"result_type": "polymer_instance", "total_count": 1, "result_set": [hit(), hit("2DEF.A")]},
                 {"result_type": "polymer_instance", "total_count": 2, "result_set": [hit(), hit()]},
                 {"result_type": "polymer_instance", "total_count": 2, "result_set": [hit(), hit("pdb_00001abc.A")]},
                 {"result_type": "polymer_instance", "total_count": 2, "result_set": [hit(), hit("2DEF.A", "1")]},
                 {"result_type": "polymer_instance", "total_count": 1,
                  "result_set": [{"identifier": "1ABC.A", "score": 1}]},
                 {"result_type": "polymer_instance", "total_count": 1, "result_set": [{
                     "identifier": "1ABC.A", "score": 1,
                     "services": [{"service_type": "sequence", "nodes": [{"original_score": 1}]}]}]}]
        for body in cases:
            with self.subTest(body=body):
                client = httpx.AsyncClient(transport=httpx.MockTransport(
                    lambda request: response(body)), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client), self.assertRaises(ValueError):
                    await search.search_structure_similarity("1ABC", "A", max_rows=1)
                self.assertTrue(client.is_closed)

    async def test_error_statuses_are_not_empty_results_or_retried(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.similarity")
        for status in (400, 404, 429, 503):
            with self.subTest(status=status):
                requests = []
                def respond(request):
                    requests.append(request)
                    return response({"message": "private provider diagnostic"}, status)
                client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client):
                    with self.assertRaisesRegex(RuntimeError, f"HTTP {status}") as raised:
                        await search.search_structure_similarity("1ABC", "A")
                self.assertNotIn("private provider diagnostic", str(raised.exception))
                self.assertEqual(len(requests), 1)
                self.assertTrue(client.is_closed)

    async def test_output_chain_identifier_is_not_restricted_by_the_input_length_limit(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.similarity")
        chain = "A" * 17
        client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: response({
            "result_type": "polymer_instance", "total_count": 1,
            "result_set": [hit("2DEF." + chain)]})), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_structure_similarity("1ABC", "A")
        self.assertEqual(result["records"][0]["label_asym_id"], chain)
        self.assertEqual(result["records"][0]["identifier"], "2DEF." + chain)
        self.assertTrue(client.is_closed)


if __name__ == "__main__":
    unittest.main()
