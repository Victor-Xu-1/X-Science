"""Direct sequence search contracts without provider or scientific workloads."""
from __future__ import annotations

import importlib
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

import httpx

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "lib"))

SEQUENCE = "ACDEFGHIKLMNPQRSTVWY" * 5
SEARCH_URL = "https://search.rcsb.org/rcsbsearch/v2/query"
DATA_URL = "https://data.rcsb.org/rest/v1/core/polymer_entity/"


def alignment(**updates):
    return {"sequence_identity": 0.95, "evalue": 0, "bitscore": 70,
            "alignment_length": 300, "query_length": 80, "query_beg": 2, "query_end": 81,
            "subject_beg": 3, "subject_end": 82, **updates}


def hit(identifier, matches=None):
    return {"identifier": identifier, "score": 0, "services": [{"service_type": "sequence",
            "nodes": [{"match_context": [alignment()] if matches is None else matches}]}]}


def entity(pdb_id="1ABC", entity_id="2"):
    return {"rcsb_id": f"{pdb_id}_{entity_id}", "rcsb_polymer_entity_container_identifiers": {
        "entry_id": pdb_id, "entity_id": entity_id, "asym_ids": ["A", "B"],
        "auth_asym_ids": ["X", "Y"]}, "rcsb_polymer_entity": {"pdbx_description": "Protein"},
        "entity_poly": {"rcsb_sample_sequence_length": 120}}


def response(data, status=200):
    return httpx.Response(status, stream=httpx.ByteStream(json.dumps(data).encode()),
                          headers={"Content-Type": "application/json"})


class PDBSequenceContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_normalizes_single_fasta_and_separates_chain_schemes(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        requests = []

        def respond(request):
            requests.append(request)
            if request.method == "POST":
                return response({"total_count": 1, "result_set": [hit("1ABC_2")]})
            self.assertEqual(str(request.url), DATA_URL + "1ABC/2")
            return response(entity())

        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_sequence(f" >query\r\n{SEQUENCE[:40].lower()}\r\n {SEQUENCE[40:]}",
                identity_cutoff=0.9, min_query_coverage=0.8)
        self.assertTrue(client.is_closed)
        self.assertEqual(len(requests), 2)
        self.assertEqual(str(requests[0].url), SEARCH_URL)
        body = json.loads(requests[0].content)
        self.assertEqual(body, {"query": {"type": "group", "logical_operator": "and", "nodes": [
            {"type": "terminal", "service": "sequence", "parameters": {"value": SEQUENCE,
             "sequence_type": "protein", "identity_cutoff": 0.9, "evalue_cutoff": 0.1}},
            {"type": "terminal", "service": "text", "parameters": {
             "attribute": "rcsb_entry_info.structure_determination_methodology",
             "operator": "exact_match", "value": "experimental"}}]}, "return_type": "polymer_entity",
            "request_options": {"paginate": {"start": 0, "rows": 100},
             "results_content_type": ["experimental"], "results_verbosity": "verbose"}})
        self.assertEqual((result["query_length"], result["total_count"], result["n_scanned"],
                          result["n_matched"], result["n_returned"], result["truncated"]),
                         (100, 1, 1, 1, 1, False))
        record = result["records"][0]
        self.assertEqual(record["asym_ids"], ["A", "B"])
        self.assertEqual(record["auth_asym_ids"], ["X", "Y"])
        self.assertEqual(record["matches"][0]["query_coverage"], 0.8)
        self.assertEqual(record["matches"][0]["evalue"], 0)

    async def test_filters_alignments_and_keeps_distinct_counts(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        requests = []

        def respond(request):
            requests.append(request)
            if request.method == "POST":
                return response({"total_count": 8, "result_set": [
                    hit("1ABC_1", [alignment(query_end=50)]),
                    hit("1ABC_2", [alignment(query_end=50), alignment()]), hit("2DEF_1")]})
            return response(entity())

        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_sequence(SEQUENCE, min_query_coverage=0.8, max_candidates=3, max_rows=1)
        self.assertTrue(client.is_closed)
        self.assertEqual(len(requests), 2)
        self.assertEqual((result["total_count"], result["n_scanned"], result["n_matched"],
                          result["n_returned"], result["truncated"]), (8, 3, 2, 1, True))
        self.assertEqual(result["records"][0]["rcsb_id"], "1ABC_2")
        self.assertEqual(len(result["records"][0]["matches"]), 1)

    async def test_crosses_fully_filtered_page_and_counts_beyond_display_cap(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        requests = []

        def respond(request):
            requests.append(request)
            if request.method == "POST":
                page = json.loads(request.content)["request_options"]["paginate"]
                if page["start"] == 0:
                    self.assertEqual(page["rows"], 100)
                    return response({"total_count": 105, "result_set": [
                        hit(f"1ABC_{index + 1}", [alignment(query_end=50)]) for index in range(100)]})
                self.assertEqual(page, {"start": 100, "rows": 5})
                return response({"total_count": 105, "result_set": [
                    hit(f"1ABC_{index + 101}") for index in range(5)]})
            return response(entity(entity_id="101"))

        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_sequence(SEQUENCE, max_candidates=105, max_rows=1, min_query_coverage=0.8)
        self.assertEqual((result["total_count"], result["n_scanned"], result["n_matched"],
                          result["n_returned"], result["truncated"]), (105, 105, 5, 1, True))
        self.assertEqual(len(requests), 3)
        self.assertTrue(client.is_closed)

    async def test_short_pages_and_later_204_preserve_incomplete_totals(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        for second in (204, 200):
            with self.subTest(second=second):
                requests = []
                def respond(request):
                    requests.append(request)
                    if len(requests) == 1:
                        return response({"total_count": 9, "result_set": [
                            hit("1ABC_2", [alignment(query_end=50)])]})
                    return response({"total_count": 9, "result_set": []}, second)
                client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client):
                    result = await search.search_sequence(SEQUENCE, max_candidates=101, min_query_coverage=0.8)
                self.assertEqual((result["total_count"], result["n_scanned"], result["truncated"]), (9, 1, True))
                self.assertEqual(json.loads(requests[1].content)["request_options"]["paginate"]["start"], 1)
                self.assertEqual(len(requests), 2)
                self.assertTrue(client.is_closed)

    async def test_short_nonempty_pages_stop_at_the_logical_page_budget(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        requests = []
        def respond(request):
            requests.append(request)
            return response({"total_count": 1000, "result_set": [
                hit(f"1ABC_{len(requests)}", [alignment(query_end=50)])]})
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_sequence(SEQUENCE, max_candidates=101, min_query_coverage=0.8)
        self.assertEqual((result["total_count"], result["n_scanned"], result["truncated"]), (1000, 2, True))
        self.assertEqual(len(requests), 2)

    async def test_missing_or_out_of_range_coverage_is_null_not_fabricated(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        for coordinates in ({"query_end": 101}, {"query_beg": 90}, {"query_end": None}):
            for cutoff in (0, 0.8):
                with self.subTest(coordinates=coordinates, cutoff=cutoff):
                    requests = []
                    def respond(request):
                        requests.append(request)
                        return response({"total_count": 1, "result_set": [hit("1ABC_2", [
                            alignment(**coordinates)])]} if request.method == "POST" else entity())
                    client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                    with patch.object(transport, "_http_client", return_value=client):
                        result = await search.search_sequence(SEQUENCE, min_query_coverage=cutoff)
                    if cutoff:
                        self.assertEqual(result["n_matched"], 0)
                        self.assertEqual(len(requests), 1)
                    else:
                        self.assertIsNone(result["records"][0]["matches"][0]["query_coverage"])
                        self.assertEqual(len(requests), 2)

    async def test_metadata_404_is_explicit_unknown_and_extended_identity_is_retained(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        requests = []
        def respond(request):
            requests.append(request)
            if request.method == "POST":
                return response({"total_count": 1, "result_set": [hit("pdb_00001abc_2")]})
            self.assertEqual(str(request.url), DATA_URL + "pdb_00001abc/2")
            return response({"error": "not_found"}, 404)
        client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_sequence(SEQUENCE)
        record = result["records"][0]
        self.assertEqual(record["rcsb_id"], "pdb_00001abc_2")
        self.assertEqual(record["metadata_error"], "not_found")
        for key in ("description", "asym_ids", "auth_asym_ids", "sequence_length"):
            self.assertIsNone(record[key])
        self.assertEqual(record["matches"][0]["evalue"], 0)

    async def test_rejects_bad_sequence_or_numeric_arguments_before_transport(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        bad_sequences = ("", "A" * 24, "A" * 10001, "A" * 50001, SEQUENCE + "-",
                         SEQUENCE + "*", SEQUENCE + "1", f">one\n{SEQUENCE}\n>two\n{SEQUENCE}", None, 4)
        bad_numbers = (("identity_cutoff", True), ("identity_cutoff", 1.1),
                       ("identity_cutoff", "0.5"), ("evalue_cutoff", 0),
                       ("evalue_cutoff", float("inf")), ("min_query_coverage", None),
                       ("min_query_coverage", float("nan")), ("max_candidates", 0),
                       ("max_candidates", 1001), ("max_candidates", 2.5),
                       ("max_rows", True), ("max_rows", 26))
        with patch.object(transport, "_http_client") as factory:
            for value in bad_sequences:
                with self.subTest(sequence=value), self.assertRaises(ValueError):
                    await search.search_sequence(value)
            for key, value in bad_numbers:
                with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                    await search.search_sequence(SEQUENCE, **{key: value})
        factory.assert_not_called()

    async def test_required_and_optional_alignment_metrics_are_strict(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        invalid = ({"sequence_identity": None}, {"sequence_identity": "0.5"}, {"sequence_identity": 1.1},
                   {"evalue": -1}, {"bitscore": True}, {"alignment_length": 1.5},
                   {"query_beg": 0}, {"mismatches": -1}, {"subject_end": "100"})
        for metrics in invalid:
            with self.subTest(metrics=metrics):
                requests = []
                def respond(request):
                    requests.append(request)
                    return response({"total_count": 1, "result_set": [hit("1ABC_2", [alignment(**metrics)])]})
                client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client), self.assertRaises(ValueError):
                    await search.search_sequence(SEQUENCE)
                self.assertEqual(len(requests), 1)
                self.assertTrue(client.is_closed)

    async def test_bad_or_wrong_identity_metadata_is_not_unknown(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        wrong = entity("2DEF", "2")
        bad_chains = entity()
        bad_chains["rcsb_polymer_entity_container_identifiers"]["auth_asym_ids"] = [False]
        for metadata in (None, [], {}, wrong, bad_chains):
            with self.subTest(metadata=metadata):
                def respond(request):
                    return response({"total_count": 1, "result_set": [hit("1ABC_2")]}
                                    if request.method == "POST" else metadata)
                client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client), self.assertRaises(ValueError):
                    await search.search_sequence(SEQUENCE)
                self.assertTrue(client.is_closed)

    async def test_204_is_empty_but_malformed_search_and_missing_context_are_errors(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: response(None, 204)), trust_env=False)
        with patch.object(transport, "_http_client", return_value=client):
            result = await search.search_sequence(SEQUENCE)
        self.assertEqual((result["total_count"], result["n_scanned"], result["records"], result["truncated"]),
                         (0, 0, [], False))
        for body in (None, {}, {"total_count": True}, {"total_count": 1, "result_set": [hit("1ABC_2", [])]},
                     {"total_count": 0, "result_set": [hit("1ABC_2")]},
                     {"total_count": 2, "result_set": [hit("1ABC_2"), hit("1ABC_2")]}):
            with self.subTest(body=body):
                client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: response(body)), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client), self.assertRaises(ValueError):
                    await search.search_sequence(SEQUENCE)

    async def test_optional_entity_objects_may_be_null_without_faking_a_metadata_404(self):
        transport = importlib.import_module("pdb_structures.async_client")
        search = importlib.import_module("pdb_structures.sequence")
        for entity_value, polymer_value in ((None, None), (None, {}), ({}, None)):
            with self.subTest(entity=entity_value, polymer=polymer_value):
                metadata = entity()
                metadata["rcsb_polymer_entity"] = entity_value
                metadata["entity_poly"] = polymer_value
                def respond(request):
                    return response({"total_count": 1, "result_set": [hit("1ABC_2")]}
                                    if request.method == "POST" else metadata)
                client = httpx.AsyncClient(transport=httpx.MockTransport(respond), trust_env=False)
                with patch.object(transport, "_http_client", return_value=client):
                    result = await search.search_sequence(SEQUENCE)
                record = result["records"][0]
                self.assertIsNone(record["description"])
                self.assertIsNone(record["sequence_length"])
                self.assertEqual(record["asym_ids"], ["A", "B"])
                self.assertEqual(record["auth_asym_ids"], ["X", "Y"])
                self.assertNotIn("metadata_error", record)
                self.assertTrue(client.is_closed)


if __name__ == "__main__":
    unittest.main()
