"""Exact UniChem contracts through the shared transport and real MCP facade."""
from __future__ import annotations

import asyncio
from copy import deepcopy
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import AsyncMock, patch

import anyio
import httpx

LIB = Path(__file__).resolve().parents[1] / "lib"
sys.path.insert(0, str(LIB))
from mcp_chemistry import server, unichem
from mcp_servers_common import ebi_http

KEY = "RMUCZJUITONUFY-UHFFFAOYSA-N"
ARGS = {"id_type": "chembl", "identifier": "CHEMBL1089"}
ROW = {"uci": 211306, "standardInchiKey": KEY, "sources": [
    {"id": 1, "compoundId": "CHEMBL1089"},
    {"id": 7, "compoundId": "CHEBI:8060"},
    {"id": 22, "compoundId": "3675"},
    {"id": 2, "compoundId": "DB00780"},
]}


def envelope(rows=None):
    rows = [deepcopy(ROW)] if rows is None else rows
    return {"response": "Success", "totalCompounds": str(len(rows)), "compounds": rows}


class _Bytes(httpx.AsyncByteStream):
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


class UniChemContracts(unittest.IsolatedAsyncioTestCase):
    async def call(self, body=None, *, args=None, status=200, raw=None, headers=None):
        raw = json.dumps(envelope() if body is None else body).encode() if raw is None else raw
        stream = _Bytes([raw])
        calls = []

        def handler(request):
            calls.append(request)
            return httpx.Response(status, headers=headers, stream=stream)

        client = httpx.AsyncClient(transport=httpx.MockTransport(handler), trust_env=False, follow_redirects=False)
        try:
            with patch.object(ebi_http, "_http_client", return_value=client):
                result = await unichem.map_compound(**(ARGS if args is None else args))
        finally:
            self.assertTrue(client.is_closed)
        return result, calls, stream

    async def test_exact_queries_post_fixed_body_and_preserve_native_records(self):
        for args in (ARGS, {"id_type": "inchikey", "identifier": KEY}):
            with self.subTest(args=args):
                result, calls, stream = await self.call(args=args)
                self.assertTrue(stream.closed)
                self.assertEqual(len(calls), 1)
                self.assertEqual(str(calls[0].url), unichem.API)
                self.assertEqual(calls[0].method, "POST")
                self.assertEqual(json.loads(calls[0].content),
                                 {"compound": KEY, "type": "inchikey"} if args["id_type"] == "inchikey"
                                 else {"compound": "CHEMBL1089", "sourceID": 1, "type": "sourceID"})
                self.assertEqual(result, {
                    "source": "UniChem", "source_url": unichem.API, **args,
                    "target_sources": ["ChEMBL", "ChEBI", "PubChem"],
                    "total_matches": 1, "records_truncated": False,
                    "records": [{"uci": 211306, "standard_inchi_key": KEY,
                                 "total_mappings": 4, "filtered_out_mappings": 1,
                                 "matching_mappings": 3, "mappings_truncated": False,
                                 "mappings": [
                                     {"source": "ChEMBL", "identifier": "CHEMBL1089",
                                      "url": "https://www.ebi.ac.uk/chembl/compound_report_card/CHEMBL1089"},
                                     {"source": "ChEBI", "identifier": "CHEBI:8060",
                                      "url": "https://www.ebi.ac.uk/chebi/CHEBI:8060"},
                                     {"source": "PubChem", "identifier": "3675",
                                      "url": "https://pubchem.ncbi.nlm.nih.gov/compound/3675"},
                                 ]}],
                })

    async def test_invalid_queries_fail_before_opening_transport(self):
        invalid = [(None, None), ("name", "aspirin"), ("chembl", "CHEMBL0"),
                   ("chembl", "CHEMBL1089,25"), ("chembl", "chembl1089"),
                   ("chembl", KEY), ("inchikey", "CHEMBL1089"),
                   ("inchikey", KEY.lower()), ("inchikey", KEY[:14]),
                   ("inchikey", KEY + "\n"), ("chembl", "CHEMBL" + "1" * 16),
                   ("chembl", ["CHEMBL1089"]), (True, "CHEMBL1089")]
        with patch.object(unichem, "ebi_json_session") as context:
            for id_type, identifier in invalid:
                with self.subTest(id_type=id_type, identifier=identifier), self.assertRaisesRegex(ValueError, "exact full"):
                    await unichem.map_compound(id_type, identifier)
            context.assert_not_called()

    async def test_explicit_absence_accepts_only_consistent_empty_envelopes(self):
        for total in (None, 0, "0"):
            body = {"response": "Not found", "compounds": []}
            if total is not None:
                body["totalCompounds"] = total
            result, _, _ = await self.call(body)
            self.assertEqual(result["records"], [])
            self.assertEqual(result["total_matches"], 0)
            self.assertIs(result["records_truncated"], False)

    async def test_structure_match_with_no_target_mapping_is_not_absence(self):
        for sources in ([], [{"id": 2, "compoundId": "DB00780"}]):
            row = {**deepcopy(ROW), "sources": sources}
            result, _, _ = await self.call(envelope([row]), args={"id_type": "inchikey", "identifier": KEY})
            self.assertEqual(result["total_matches"], 1)
            self.assertEqual(result["records"][0]["mappings"], [])
            self.assertEqual(result["records"][0]["matching_mappings"], 0)
            self.assertEqual(result["records"][0]["filtered_out_mappings"], len(sources))

    async def test_structure_truncation_keeps_true_count_and_validates_hidden_rows(self):
        rows = [{**deepcopy(ROW), "uci": index + 1} for index in range(6)]
        result, _, _ = await self.call(envelope(rows))
        self.assertEqual(result["total_matches"], 6)
        self.assertIs(result["records_truncated"], True)
        self.assertEqual([row["uci"] for row in result["records"]], [1, 2, 3, 4, 5])
        rows[5]["sources"] = [{"id": 1, "compoundId": "CHEMBL25"}]
        with self.assertRaisesRegex(ValueError, "inconsistent with"):
            await self.call(envelope(rows))

    async def test_mapping_filtering_truncation_and_urls_remain_distinct(self):
        row = deepcopy(ROW)
        row["sources"] = [row["sources"][0]] + [
            {"id": 22, "compoundId": str(index + 1), "url": "https://untrusted.invalid/"}
            for index in range(25)
        ] + [{"id": 50, "compoundId": f"trial{index}"} for index in range(1000)]
        result, _, _ = await self.call(envelope([row]))
        projected = result["records"][0]
        self.assertEqual((projected["total_mappings"], projected["filtered_out_mappings"],
                          projected["matching_mappings"]), (1026, 1000, 26))
        self.assertIs(projected["mappings_truncated"], True)
        self.assertEqual(len(projected["mappings"]), 20)
        self.assertNotIn("untrusted", json.dumps(result))
        self.assertNotIn("trial", json.dumps(result))
        self.assertLess(len(json.dumps(result)), 5000)

    async def test_invalid_target_mapping_beyond_output_cap_is_not_ignored(self):
        for source in ({"id": 1, "compoundId": "CHEMBL0"}, {"id": 7, "compoundId": "CHEBI:0"},
                       {"id": 22, "compoundId": "0"}, {"id": 22, "compoundId": "../escape"}):
            row = deepcopy(ROW)
            row["sources"] += [{"id": 22, "compoundId": str(index + 1)} for index in range(25)]
            row["sources"].append(source)
            with self.subTest(source=source), self.assertRaisesRegex(ValueError, "invalid mapped identifier"):
                await self.call(envelope([row]))

    async def test_wrong_requested_identity_is_an_error_for_each_query_type(self):
        for args in ({"id_type": "inchikey", "identifier": "BSYNRYMUTXBXSQ-UHFFFAOYSA-N"},
                     {"id_type": "chembl", "identifier": "CHEMBL25"}):
            with self.subTest(args=args), self.assertRaisesRegex(ValueError, "inconsistent with"):
                await self.call(args=args)

    async def test_invalid_or_contradictory_envelopes_are_not_empty_matches(self):
        bodies = [{}, [], {"response": "Unknown", "compounds": []},
                  {"response": "Success", "compounds": []},
                  {**envelope(), "response": "Not found"},
                  {**envelope(), "totalCompounds": 2},
                  {**envelope(), "totalCompounds": True},
                  {**envelope(), "totalCompounds": None},
                  {**envelope(), "totalCompounds": "1.0"},
                  {**envelope(), "totalCompounds": "1" * 11},
                  {**envelope(), "totalCompounds": 1.5},
                  {**envelope(), "totalCompounds": 1 << 53},
                  {"response": "Not found", "compounds": [], "totalCompounds": 1}]
        for body in bodies:
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, "UniChem returned"):
                await self.call(body)

    async def test_native_integer_and_source_fields_are_strict_even_when_filtered(self):
        invalid = []
        for value in (True, 0, -1, "1", 1.5, 1 << 53):
            invalid.append({**deepcopy(ROW), "uci": value})
            row = deepcopy(ROW)
            row["sources"][3]["id"] = value
            invalid.append(row)
        for value in (None, [], "bad", KEY + "\n"):
            invalid.append({**deepcopy(ROW), "standardInchiKey": value})
        for value in (None, [], "", "x" * 1001, "\U0001f9ea" * 501):
            row = deepcopy(ROW)
            row["sources"][3]["compoundId"] = value
            invalid.append(row)
        for row in invalid:
            with self.subTest(row=row), self.assertRaisesRegex(ValueError, "invalid compound data"):
                await self.call(envelope([row]))

    async def test_exact_integral_json_numbers_remain_interoperable(self):
        row = deepcopy(ROW)
        row["uci"] = 211306.0
        row["sources"][0]["id"] = 1.0
        result, _, _ = await self.call({**envelope([row]), "totalCompounds": 1.0})
        self.assertIs(type(result["records"][0]["uci"]), int)
        self.assertEqual(result["records"][0]["uci"], 211306)

    async def test_native_array_bounds_are_not_silent_truncation(self):
        for body in (envelope([deepcopy(ROW) for _ in range(101)]),
                     envelope([{**deepcopy(ROW), "sources": [{"id": 2, "compoundId": "x"}] * 10_001}]),
                     envelope([{**deepcopy(ROW), "sources": {}}])):
            with self.subTest(compound_count=len(body["compounds"])), self.assertRaisesRegex(ValueError, "invalid compound data"):
                await self.call(body)

    async def test_http_failure_and_redirect_are_errors_not_no_match(self):
        for status in (302, 404, 429, 500):
            with self.subTest(status=status), self.assertRaisesRegex(RuntimeError, f"HTTP {status}"):
                await self.call({"error": "controlled failure"}, status=status,
                                headers={"Location": "https://untrusted.invalid/"})

    async def test_shared_strict_json_and_byte_budget_survive_adapter(self):
        for raw in (b'null', b'<html>error</html>', b'{"response":"Not found","response":"Success","compounds":[]}',
                    b'{"uci":NaN}', b'{"uci":1e999}', b'\xff', b' ' * 1_000_001):
            with self.subTest(prefix=raw[:20]), self.assertRaisesRegex(ValueError, "invalid JSON|byte budget|invalid compound data"):
                await self.call(raw=raw)

    async def test_whole_call_timeout_and_cancel_close_the_shared_transport(self):
        for cancel in (False, True):
            with self.subTest(cancel=cancel):
                stream = _Bytes(stall=True)
                client = httpx.AsyncClient(transport=httpx.MockTransport(
                    lambda request: httpx.Response(200, stream=stream)), trust_env=False)
                with patch.object(ebi_http, "_http_client", return_value=client), \
                        patch.object(ebi_http, "CALL_TIMEOUT_SECONDS", 0.01 if not cancel else 1):
                    task = asyncio.create_task(unichem.map_compound(**ARGS))
                    if cancel:
                        with anyio.fail_after(1):
                            await stream.started.wait()
                            task.cancel()
                            with self.assertRaises(asyncio.CancelledError):
                                await task
                    else:
                        with self.assertRaisesRegex(TimeoutError, "whole-call deadline"):
                            await task
                self.assertTrue(client.is_closed)
                self.assertTrue(stream.closed)

    async def test_real_mcp_catalog_retains_existing_tools_and_strict_identifier_fields(self):
        tools = {tool.name: tool.model_dump(by_alias=True) for tool in await server.mcp.list_tools()}
        original = {"pubchem_search_compounds", "pubchem_get_compounds", "pubchem_similarity_search",
                    "pubchem_get_bioassay_summary", "pubchem_get_safety", "chebi_search",
                    "chebi_get_entity", "chebi_get_ontology", "rhea_search_reactions", "rhea_get_reaction",
                    "bindingdb_ligands_by_target", "bindingdb_targets_by_compound"}
        self.assertTrue(original.issubset(tools))
        tool = tools["unichem_map_compound"]
        self.assertIs(tool["annotations"]["readOnlyHint"], True)
        schema = tool["inputSchema"]
        self.assertEqual(schema, unichem.UNICHEM_INPUT_SCHEMA)
        self.assertIs(schema["additionalProperties"], False)
        self.assertEqual(set(schema["properties"]), {"id_type", "identifier"})
        self.assertEqual(set(schema["required"]), {"id_type", "identifier"})
        self.assertEqual(schema["properties"]["id_type"]["enum"], ["inchikey", "chembl"])
        self.assertEqual(schema["properties"]["id_type"]["examples"], ["chembl"])
        identifier = schema["properties"]["identifier"]
        self.assertEqual(identifier["type"], "string")
        self.assertEqual(identifier["maxLength"], 27)
        self.assertEqual(identifier["pattern"], unichem.IDENTIFIER_PATTERN)
        self.assertEqual(identifier["examples"], ["CHEMBL1089"])

    async def test_declared_schema_rejects_wrong_kind_and_extra_before_adapter(self):
        invalid = [{"id_type": "inchikey", "identifier": "CHEMBL1089"},
                   {"id_type": "chembl", "identifier": KEY}, {**ARGS, "extra": "ignored"},
                   {"id_type": "chembl", "identifier": 1089}]
        with patch.object(server, "_unichem_map_compound", new_callable=AsyncMock) as adapter, \
                patch.object(ebi_http, "_http_client") as transport:
            for args in invalid:
                with self.subTest(args=args):
                    result = (await server.mcp.call_tool("unichem_map_compound", args)).model_dump(by_alias=True)
                    self.assertIs(result["isError"], True)
                    self.assertEqual(result["content"][0]["text"],
                                     "Tool arguments do not match the declared input schema")
            adapter.assert_not_awaited()
            transport.assert_not_called()

    async def test_discovered_schema_cannot_mutate_later_discovery_or_validation(self):
        tools = {tool.name: tool.model_dump(by_alias=True) for tool in await server.mcp.list_tools()}
        schema = tools["unichem_map_compound"]["inputSchema"]
        schema["allOf"][0]["then"]["properties"]["identifier"]["pattern"] = ".*"
        schema["properties"]["identifier"]["examples"].append("not an identifier")
        schema["additionalProperties"] = True
        again = {tool.name: tool.model_dump(by_alias=True) for tool in await server.mcp.list_tools()}
        self.assertEqual(again["unichem_map_compound"]["inputSchema"], unichem.UNICHEM_INPUT_SCHEMA)
        with patch.object(server, "_unichem_map_compound", new_callable=AsyncMock) as adapter:
            result = (await server.mcp.call_tool("unichem_map_compound", {**ARGS, "extra": 1})).model_dump(by_alias=True)
            self.assertIs(result["isError"], True)
            adapter.assert_not_awaited()

    async def test_real_stdio_mcp_call_preserves_wire_success_and_validation_error(self):
        from mcp import ClientSession, StdioServerParameters
        from mcp.client.stdio import stdio_client

        # The ordinary registered SDK facade is used; only its public EBI HTTP
        # dependency is controlled. No provider socket, scientific worker or
        # second MCP handler is created by this synthetic contract test.
        script = f"""
import json
import httpx
from mcp_servers_common import ebi_http
from mcp_chemistry import server
class Body(httpx.AsyncByteStream):
    async def __aiter__(self):
        yield {json.dumps(envelope()).encode()!r}
def handle(request):
    assert str(request.url) == {unichem.API!r}
    assert request.method == 'POST'
    assert json.loads(request.content) == {{'compound': 'CHEMBL1089', 'sourceID': 1, 'type': 'sourceID'}}
    return httpx.Response(200, stream=Body())
ebi_http._http_client = lambda: httpx.AsyncClient(transport=httpx.MockTransport(handle), trust_env=False)
server.mcp.run()
"""
        parameters = StdioServerParameters(command=sys.executable, args=["-B", "-c", script],
                                          env={"PYTHONPATH": str(LIB), "PYTHONDONTWRITEBYTECODE": "1"})
        with anyio.fail_after(10):
            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    listing = (await session.list_tools()).model_dump(by_alias=True)
                    tool = next(tool for tool in listing["tools"] if tool["name"] == "unichem_map_compound")
                    self.assertIs(tool["annotations"]["readOnlyHint"], True)
                    self.assertEqual(tool["inputSchema"], unichem.UNICHEM_INPUT_SCHEMA)
                    result = (await session.call_tool("unichem_map_compound", ARGS)).model_dump(by_alias=True)
                    self.assertIs(result["isError"], False)
                    self.assertEqual(result["structuredContent"]["total_matches"], 1)
                    for args in ({"id_type": "inchikey", "identifier": "CHEMBL1089"}, {**ARGS, "extra": True}):
                        bad = (await session.call_tool("unichem_map_compound", args)).model_dump(by_alias=True)
                        self.assertIs(bad["isError"], True)
                        self.assertEqual(bad["content"][0]["text"],
                                         "Tool arguments do not match the declared input schema")


if __name__ == "__main__":
    unittest.main()
