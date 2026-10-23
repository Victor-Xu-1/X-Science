"""Actual packaged stdio discovery/call results, with synthetic provider HTTP only."""
from __future__ import annotations

from contextlib import asynccontextmanager
import json
from pathlib import Path
import sys
import unittest

import anyio
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

ROOT = Path(__file__).resolve().parents[1]
CHILD = r'''
import json
from pathlib import Path
import sys
import httpx

root = Path(sys.argv[1])
sys.path.insert(0, str(root / "lib"))
from pdb_structures import async_client

class Body(httpx.AsyncByteStream):
    def __init__(self, value):
        self.value = json.dumps(value).encode()
    async def __aiter__(self):
        yield self.value

async def handle(request):
    assert request.url.scheme == "https"
    if request.method == "POST":
        assert str(request.url) == "https://search.rcsb.org/rcsbsearch/v2/query"
        payload = json.loads(request.content)
        query = payload["query"]
        if query.get("service") == "structure":
            assert query["parameters"]["value"] == {"entry_id": "1CLL", "asym_id": "a"}
            assert payload["return_type"] == "polymer_instance"
            value = {"result_type": "polymer_instance", "total_count": 3,
                     "result_set": [{"identifier": "1CLL.a", "score": 0.0,
                                     "services": [{"service_type": "structure", "nodes": [{"original_score": 0.9}]}]}]}
        else:
            assert query["logical_operator"] == "and"
            sequence = query["nodes"][0]["parameters"]["value"]
            value = {"total_count": 1, "result_set": [{"identifier": "1TUP_1", "score": 1.0,
                "services": [{"service_type": "sequence", "nodes": [{"match_context": [{
                    "sequence_identity": 1.0, "evalue": 0.0, "bitscore": 42.0,
                    "query_beg": 1, "query_end": len(sequence), "subject_beg": 1,
                    "subject_end": len(sequence), "alignment_length": len(sequence)}]}]}]}]}
    else:
        assert request.method == "GET"
        assert str(request.url) == "https://data.rcsb.org/rest/v1/core/polymer_entity/1TUP/1"
        value = {"rcsb_id": "1TUP_1", "rcsb_polymer_entity": {"pdbx_description": "Synthetic provider record"},
                 "rcsb_polymer_entity_container_identifiers": {
                     "entry_id": "1TUP", "entity_id": "1", "asym_ids": ["A"], "auth_asym_ids": ["Z"]},
                 "entity_poly": {"rcsb_sample_sequence_length": 40}}
    return httpx.Response(200, headers={"content-type": "application/json"}, stream=Body(value))

async_client._http_client = lambda *a, **kw: httpx.AsyncClient(transport=httpx.MockTransport(handle), follow_redirects=False)
sys.path.insert(0, str(root))
import run_server
sys.argv = [str(root / "run_server.py"), "mcp_structures_interactions"]
run_server.main()
'''


@asynccontextmanager
async def packaged_session():
    params = StdioServerParameters(command=sys.executable, args=["-B", "-c", CHILD, str(ROOT)],
                                   env={"PYTHONDONTWRITEBYTECODE": "1"})
    with anyio.fail_after(20):
        async with stdio_client(params) as (read, write):
            # The public SDK1 and SDK2 timeout parameter uses different types.
            # The enclosing anyio deadline bounds this entire actual session.
            async with ClientSession(read, write) as session:
                await session.initialize()
                yield session


class PDBSearchProtocolContract(unittest.IsolatedAsyncioTestCase):
    async def test_real_packaged_discovery_keeps_old_tools_and_publishes_one_strict_contract(self):
        async with packaged_session() as session:
            tools = (await session.list_tools()).model_dump(by_alias=True)["tools"]
            names = [tool["name"] for tool in tools]
            self.assertEqual(len(names), len(set(names)))
            self.assertIn("pdb_select_latest_liganded_structure", names)
            self.assertIn("alphafold_check_coverage", names)
            for name in ("pdb_search_sequence", "pdb_search_structure_similarity"):
                tool = next(tool for tool in tools if tool["name"] == name)
                self.assertIs(tool["annotations"]["readOnlyHint"], True)
                self.assertIs(tool["inputSchema"]["additionalProperties"], False)

    async def test_invalid_public_calls_are_errors_before_transport_or_coercion(self):
        async with packaged_session() as session:
            for name, args in (
                ("pdb_search_structure_similarity", {"pdb_id": "1CLL", "label_asym_id": "a", "url": "private sentinel"}),
                ("pdb_search_structure_similarity", {"pdb_id": "1CLL", "label_asym_id": "a", "max_rows": True}),
                ("pdb_search_sequence", {"sequence": "ACDEFGHIKLMNPQRSTVWYB" * 2, "identity_cutoff": "0.3"}),
                ("pdb_search_sequence", {"sequence": "ACDEFGHIKLMNPQRSTVWYB" * 2, "max_candidates": 0}),
            ):
                with self.subTest(name=name, arguments=set(args)):
                    result = (await session.call_tool(name, args)).model_dump(by_alias=True)
                    self.assertIs(result["isError"], True)
                    self.assertNotIn("private sentinel", json.dumps(result))

    async def test_valid_protocol_returns_actual_structured_results_for_both_searches(self):
        async with packaged_session() as session:
            similarity = (await session.call_tool("pdb_search_structure_similarity", {
                "pdb_id": "1cll", "label_asym_id": "a", "max_rows": 7,
            })).model_dump(by_alias=True)
            self.assertIs(similarity["isError"], False)
            value = similarity["structuredContent"]
            self.assertIsInstance(value, dict)
            self.assertEqual(value["query"]["label_asym_id"], "a")
            self.assertEqual(value["total_count"], 3)
            self.assertIs(value["truncated"], True)
            self.assertEqual(value["records"][0]["relevance_score"], 0.0)
            sequence = (await session.call_tool("pdb_search_sequence", {
                "sequence": ">synthetic single record\n" + "acdefghiklmnpqrstvwYB" * 2,
                "min_query_coverage": 1.0,
            })).model_dump(by_alias=True)
            self.assertIs(sequence["isError"], False)
            value = sequence["structuredContent"]
            self.assertIsInstance(value, dict)
            self.assertEqual((value["n_scanned"], value["n_matched"], value["n_returned"]), (1, 1, 1))
            self.assertEqual(value["records"][0]["asym_ids"], ["A"])
            self.assertEqual(value["records"][0]["auth_asym_ids"], ["Z"])
            self.assertEqual(value["records"][0]["matches"][0]["query_coverage"], 1.0)


if __name__ == "__main__":
    unittest.main()
