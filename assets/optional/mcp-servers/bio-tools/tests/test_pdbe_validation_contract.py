"""PDBe source/HTTP/MCP contracts with synthetic responses, never a live query."""
from pathlib import Path
import asyncio
from copy import deepcopy
import json
import sys
import unittest
from unittest.mock import patch

import anyio
import httpx

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "lib"))
from mcp_servers_common import ebi_http
from mcp_structures_interactions import pdbe, server

ENTRY = {"title": "Synthetic experimental structure", "experimental_method": ["X-ray diffraction"]}
QUALITY = {"geometry_quality": 65.48, "data_quality": 94.74, "overall_quality": 74.71,
           "experiment_data_available": True}
STATS = {"protein_sidechains": {"num_checked": 123, "num_outliers": 3, "percent_outliers": "2.44"},
         "rna_suite": {"num_checked": 0, "num_outliers": 0, "percent_outliers": None}}
MISSING = {"message": "Requested endpoint does not contain any data"}
BASE = "https://www.ebi.ac.uk/pdbe/api"
URLS = {"entry": f"{BASE}/pdb/entry/summary/1cbs",
        "quality_scores": f"{BASE}/validation/summary_quality_scores/entry/1cbs",
        "validation_stats": f"{BASE}/validation/key_validation_stats/entry/1cbs"}
EXISTING = {
    "emdb_get_entries", "emdb_search_entries", "emdb_get_entry_section", "emdb_get_validation",
    "complexportal_get_complexes", "complexportal_search_by_participant", "intact_fetch_interactions",
    "intact_get_interactor", "intact_get_interaction_details", "intact_build_network",
    "pdb_search_structures", "pdb_get_structures", "pdb_get_entities", "pdb_get_ligands",
    "pdb_select_latest_liganded_structure", "alphafold_get_prediction", "alphafold_check_coverage",
}


class _Body(httpx.AsyncByteStream):
    def __init__(self, value=b"", *, stall=False):
        self.value, self.stall, self.closed = value, stall, False
        self.started = asyncio.Event()

    async def __aiter__(self):
        self.started.set()
        if self.value:
            yield self.value
        if self.stall:
            await anyio.sleep_forever()

    async def aclose(self):
        self.closed = True


class PDBeValidationContracts(unittest.IsolatedAsyncioTestCase):
    def transport(self, overrides=None):
        rows = {"entry": (200, {"1cbs": deepcopy([ENTRY])}),
                "quality_scores": (200, {"1cbs": deepcopy(QUALITY)}),
                "validation_stats": (200, {"1cbs": deepcopy(STATS)})}
        rows.update(overrides or {})
        calls, bodies = [], []

        def handler(request):
            calls.append((request.method, str(request.url)))
            key = next((name for name, url in URLS.items() if url == str(request.url)), None)
            self.assertIsNotNone(key, "Only the three fixed normalized endpoints may be read")
            status, value = rows[key]
            body = value if isinstance(value, _Body) else _Body(
                value if isinstance(value, bytes) else json.dumps(value, allow_nan=False).encode())
            bodies.append(body)
            return httpx.Response(status, stream=body)

        client = httpx.AsyncClient(transport=httpx.MockTransport(handler), trust_env=False)
        return client, calls, bodies

    async def query(self, overrides=None):
        client, calls, bodies = self.transport(overrides)
        with patch.object(ebi_http, "_http_client", return_value=client):
            result = await pdbe.fetch_validation("1CBS")
        self.assertTrue(client.is_closed)
        self.assertTrue(all(body.closed for body in bodies))
        return result, calls

    async def test_normalizes_exact_id_and_preserves_native_metrics(self):
        result, calls = await self.query()
        self.assertEqual(result, {
            "source": "PDBe", "pdb_id": "1cbs",
            "source_url": "https://www.ebi.ac.uk/pdbe/entry/pdb/1cbs", "entry_found": True,
            "entry": ENTRY, "quality_scores": QUALITY, "validation_stats": STATS, "source_urls": URLS,
        })
        self.assertEqual(calls[0], ("GET", URLS["entry"]))
        self.assertCountEqual(calls, [("GET", url) for url in URLS.values()])
        self.assertEqual(len(calls), 3)

    async def test_real_mcp_catalog_preserves_existing_surface(self):
        tools = {tool.name: tool.model_dump(by_alias=True) for tool in await server.mcp.list_tools()}
        self.assertTrue(EXISTING.issubset(tools))
        wire = tools["pdbe_get_validation"]
        self.assertIs(wire["annotations"]["readOnlyHint"], True)
        schema = wire["inputSchema"]
        self.assertEqual(schema, pdbe.PDBE_INPUT_SCHEMA)
        self.assertIs(schema["additionalProperties"], False)
        self.assertEqual(schema["required"], ["pdb_id"])
        self.assertEqual(set(schema["properties"]), {"pdb_id"})
        self.assertEqual(schema["properties"]["pdb_id"]["pattern"], pdbe.PDB_ID_PATTERN)
        self.assertEqual(schema["properties"]["pdb_id"]["maxLength"], 4)
        self.assertEqual(schema["properties"]["pdb_id"]["examples"], ["1cbs"])
        # Successful SDK calls are asserted on their actual stdio wire in
        # test_ebi_catalog_contract, not a version-specific direct-call shape.

    async def test_invalid_identifiers_fail_before_allocating_transport(self):
        for value in (None, True, 1, [], "", "0000", "1cb", "1cbs\n", " 1cbs", "1cbs/other",
                      "1cbs,1d3z", "pdb_00001cbs", "https://foreign.invalid/1cbs"):
            with self.subTest(value=value), patch.object(ebi_http, "_http_client") as create:
                with self.assertRaisesRegex(ValueError, "four-character PDB ID"):
                    await pdbe.fetch_validation(value)
                create.assert_not_called()
                response = await server.mcp.call_tool("pdbe_get_validation", {"pdb_id": value})
                self.assertTrue(response.model_dump(by_alias=True)["isError"])
                create.assert_not_called()
        for arguments in ({}, {"pdb_id": "1cbs", "unused": "not-admitted"}):
            with self.subTest(arguments=arguments), patch.object(ebi_http, "_http_client") as create:
                response = await server.mcp.call_tool("pdbe_get_validation", arguments)
                self.assertTrue(response.model_dump(by_alias=True)["isError"])
                create.assert_not_called()

    async def test_confirmed_absence_does_not_hide_existing_entry_or_issue_more_reads(self):
        result, calls = await self.query({"entry": (404, MISSING)})
        self.assertFalse(result["entry_found"])
        self.assertEqual(calls, [("GET", URLS["entry"])])
        for name in ("entry", "quality_scores", "validation_stats"):
            self.assertIsNone(result[name])
        for name in ("quality_scores", "validation_stats"):
            with self.subTest(report=name):
                result, calls = await self.query({name: (404, MISSING)})
                self.assertTrue(result["entry_found"])
                self.assertEqual(result["entry"], ENTRY)
                self.assertIsNone(result[name])
                self.assertIsNotNone(result["validation_stats" if name == "quality_scores" else "quality_scores"])
                self.assertEqual(len(calls), 3)

    async def test_methods_nulls_and_unknown_availability_remain_distinct(self):
        for method in ("Solution NMR", "Electron Microscopy"):
            with self.subTest(method=method):
                quality = {**QUALITY, "data_quality": None, "experiment_data_available": "unknown"}
                entry = {**ENTRY, "experimental_method": [method]}
                result, _ = await self.query({"entry": (200, {"1cbs": [entry]}),
                                             "quality_scores": (200, {"1cbs": quality})})
                self.assertEqual(result["quality_scores"], quality)
                self.assertEqual(result["entry"], entry)
        result, _ = await self.query({"quality_scores": (200, {"1cbs": {
            "geometry_quality": None, "data_quality": 0, "overall_quality": None,
            "experiment_data_available": None}})})
        self.assertEqual(result["quality_scores"]["data_quality"], 0)
        self.assertIsNone(result["quality_scores"]["geometry_quality"])
        bounded = {"title": "\U0001f9ec" * 2000, "experimental_method": ["\U0001f9ec" * 100]}
        result, _ = await self.query({"entry": (200, {"1cbs": [bounded]})})
        self.assertEqual(result["entry"], bounded)

    async def test_malformed_roots_summaries_and_quality_are_not_missing(self):
        invalid = [
            ("entry", {}), ("entry", {"2cbs": [ENTRY]}), ("entry", {"1cbs": []}),
            ("entry", {"1cbs": [ENTRY, ENTRY]}),
            ("entry", {"1cbs": [{**ENTRY, "title": 1}]}),
            ("entry", {"1cbs": [{**ENTRY, "title": "x" * 4001}]}),
            ("entry", {"1cbs": [{**ENTRY, "title": "\U0001f9ec" * 2001}]}),
            ("entry", {"1cbs": [{**ENTRY, "experimental_method": []}]}),
            ("entry", {"1cbs": [{**ENTRY, "experimental_method": [False]}]}),
            ("entry", {"1cbs": [{**ENTRY, "experimental_method": ["x" * 201]}]}),
            ("entry", {"1cbs": [{**ENTRY, "experimental_method": ["\U0001f9ec" * 101]}]}),
            ("quality_scores", {"1cbs": {**QUALITY, "data_quality": 101}}),
            ("quality_scores", {"1cbs": {**QUALITY, "data_quality": -1}}),
            ("quality_scores", {"1cbs": {**QUALITY, "data_quality": True}}),
            ("quality_scores", {"1cbs": {**QUALITY, "experiment_data_available": "false"}}),
            ("quality_scores", {"1cbs": {**QUALITY, "experiment_data_available": 1}}),
            ("quality_scores", {"1cbs": {"data_quality": None}}),
        ]
        for name, body in invalid:
            with self.subTest(name=name, body=body), self.assertRaisesRegex(ValueError, "invalid entry data"):
                await self.query({name: (200, body)})

    async def test_statistics_reject_booleans_precision_loss_and_inconsistent_counts(self):
        row = {"num_checked": 2, "num_outliers": 1, "percent_outliers": "50"}
        invalid = [{}, {"irrelevant": row}, {"bonds": None}]
        invalid.extend({"bonds": {**row, **change}} for change in (
            {"num_checked": True}, {"num_outliers": False}, {"num_checked": -1},
            {"num_checked": 1.5}, {"num_outliers": 3}, {"num_checked": 2**53},
            {"rmsz": True}, {"rmsz": -0.1}, {"percent_outliers": 50},
            {"percent_outliers": "100.01"}, {"percent_outliers": "NaN"},
            {"percent_outliers": "1e1"}, {"percent_outliers": " 50"},
            {"percent_outliers": "50\n"}, {"percent_outliers": "0" * 31},
        ))
        for value in invalid:
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "invalid entry data"):
                await self.query({"validation_stats": (200, {"1cbs": value})})

    async def test_statistics_keep_rmsz_null_and_percentage_bytes_strip_unneeded_fields(self):
        row = {"rmsz": None, "num_checked": 2.0, "num_outliers": 1, "percent_outliers": "050.00",
               "extra": "unneeded"}
        result, _ = await self.query({
            "entry": (200, {"1cbs": [{**ENTRY, "authors": "unneeded"}], "other": "unneeded"}),
            "quality_scores": (200, {"1cbs": {**QUALITY, "extra": "unneeded"}}),
            "validation_stats": (200, {"1cbs": {"bonds": row, "extra": "unneeded"}}),
        })
        self.assertEqual(result["validation_stats"], {"bonds": {
            "rmsz": None, "num_checked": 2, "num_outliers": 1, "percent_outliers": "050.00"}})
        self.assertNotIn("unneeded", json.dumps(result))

    async def test_http_failures_unexpected_404_and_bad_json_are_not_empty_success(self):
        cases = [("entry", 404, {"error": "wrong route"}, "unexpected not-found"),
                 ("entry", 429, {"error": "synthetic private body"}, "HTTP 429"),
                 ("quality_scores", 500, {"error": "synthetic private body"}, "HTTP 500"),
                 ("entry", 200, b"<html>error</html>", "invalid JSON"),
                 ("entry", 200, b" " * 100001, "byte budget"),
                 ("quality_scores", 200, b'{"1cbs":{"data_quality":1e999}}', "invalid JSON")]
        for name, status, body, expected in cases:
            client, _, bodies = self.transport({name: (status, body)})
            with self.subTest(name=name, status=status), patch.object(ebi_http, "_http_client", return_value=client):
                with self.assertRaisesRegex(Exception, expected) as raised:
                    await pdbe.fetch_validation("1CBS")
                self.assertNotIn("synthetic private body", str(raised.exception))
            self.assertTrue(client.is_closed)
            self.assertTrue(all(value.closed for value in bodies))

    async def test_one_shared_deadline_includes_validation_reads_and_closes_sockets(self):
        stalled = _Body(stall=True)
        client, calls, bodies = self.transport({"quality_scores": (200, stalled)})
        with patch.object(ebi_http, "_http_client", return_value=client), \
                patch.object(ebi_http, "CALL_TIMEOUT_SECONDS", 0.03):
            with self.assertRaisesRegex(TimeoutError, "whole-call deadline"):
                await pdbe.fetch_validation("1CBS")
        self.assertEqual(calls[0], ("GET", URLS["entry"]))
        self.assertTrue(stalled.started.is_set())
        self.assertTrue(client.is_closed)
        self.assertTrue(all(body.closed for body in bodies))

    async def test_cancellation_during_reports_propagates_and_closes_sockets(self):
        stalled = _Body(stall=True)
        client, _, bodies = self.transport({"validation_stats": (200, stalled)})
        with patch.object(ebi_http, "_http_client", return_value=client):
            task = asyncio.create_task(pdbe.fetch_validation("1CBS"))
            with anyio.fail_after(1):
                await stalled.started.wait()
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await task
        self.assertTrue(client.is_closed)
        self.assertTrue(all(body.closed for body in bodies))


if __name__ == "__main__":
    print(f"Test interpreter: {sys.executable}", flush=True)
    unittest.main()
