"""Real existing MCP facade discovery, independent of Python SDK member naming."""
from pathlib import Path
import json
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import anyio
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

LIB = Path(__file__).resolve().parents[1] / "lib"
sys.path.insert(0, str(LIB))
from mcp_chemistry import server as chemistry
from mcp_structures_interactions import server as structures


class EBIFacadeContracts(unittest.IsolatedAsyncioTestCase):
    async def test_structures_exposes_pdbe_validation_without_replacing_existing_methods(self):
        tools = {tool.name: tool.model_dump(by_alias=True) for tool in await structures.mcp.list_tools()}
        self.assertIn("pdb_get_structures", tools)
        self.assertIn("pdbe_get_validation", sorted(tools))
        self.assertIs(tools["pdbe_get_validation"]["annotations"]["readOnlyHint"], True)
        schema = tools["pdbe_get_validation"]["inputSchema"]
        self.assertEqual(schema["required"], ["pdb_id"])
        self.assertEqual(set(schema["properties"]), {"pdb_id"})

    async def test_chemistry_exposes_unichem_mapping_without_replacing_existing_methods(self):
        tools = {tool.name: tool.model_dump(by_alias=True) for tool in await chemistry.mcp.list_tools()}
        self.assertIn("pubchem_search_compounds", tools)
        self.assertIn("unichem_map_compound", sorted(tools))
        self.assertIs(tools["unichem_map_compound"]["annotations"]["readOnlyHint"], True)
        schema = tools["unichem_map_compound"]["inputSchema"]
        self.assertEqual(set(schema["required"]), {"id_type", "identifier"})
        self.assertEqual(set(schema["properties"]), {"id_type", "identifier"})

    async def test_real_packaged_stdio_and_controlled_http_for_both_existing_connectors(self):
        calls = []
        key = "A" * 14 + "-" + "B" * 10 + "-C"
        class Handler(BaseHTTPRequestHandler):
            def send(self, data):
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps(data).encode())
            def do_GET(self):
                calls.append(("GET", self.path, None))
                if "/pdb/entry/summary/" in self.path:
                    self.send({"1cbs": [{"title": "Synthetic entry", "experimental_method": ["Solution NMR"]}]})
                elif "/summary_quality_scores/" in self.path:
                    self.send({"1cbs": {"geometry_quality": 42.0, "data_quality": None,
                        "overall_quality": None, "experiment_data_available": "unknown"}})
                else:
                    self.send({"1cbs": {"rna_suite": {"num_checked": 0, "num_outliers": 0, "percent_outliers": None}}})
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                calls.append(("POST", self.path, body))
                self.send({"response": "Success", "totalCompounds": "1", "compounds": [{"uci": 1,
                    "standardInchiKey": key, "sources": [{"id": 1, "compoundId": "CHEMBL1"}, {"id": 22, "compoundId": "42"}]}]})
            def log_message(self, *_args):
                pass
        upstream = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        worker = threading.Thread(target=upstream.serve_forever, daemon=True)
        worker.start()
        wrapper = '''import sys, importlib
import httpx
sys.path.insert(0, sys.argv[1])
from mcp_servers_common import ebi_http
server = importlib.import_module(sys.argv[3] + '.server')
class Mapped(httpx.AsyncBaseTransport):
    def __init__(self):
        self.transport = httpx.AsyncHTTPTransport()
    async def handle_async_request(self, request):
        assert request.url.scheme == 'https' and request.url.host == 'www.ebi.ac.uk'
        mapped = httpx.Request(request.method, sys.argv[2] + request.url.raw_path.decode(), headers=request.headers, content=await request.aread())
        return await self.transport.handle_async_request(mapped)
    async def aclose(self):
        await self.transport.aclose()
ebi_http._http_client = lambda: httpx.AsyncClient(transport=Mapped(), trust_env=False, follow_redirects=False)
server.main()
'''
        try:
            for package, method, arguments in (
                ("mcp_structures_interactions", "pdbe_get_validation", {"pdb_id": "1CBS"}),
                ("mcp_chemistry", "unichem_map_compound", {"id_type": "chembl", "identifier": "CHEMBL1"}),
            ):
                params = StdioServerParameters(command=sys.executable,
                    args=["-B", "-c", wrapper, str(LIB), f"http://127.0.0.1:{upstream.server_port}", package],
                    env={"PYTHONDONTWRITEBYTECODE": "1", "PYTHONPATH": str(LIB)})
                with anyio.fail_after(15):
                    async with stdio_client(params) as (read, write):
                        async with ClientSession(read, write) as session:
                            await session.initialize()
                            tool = next(tool for tool in (await session.list_tools()).tools if tool.name == method).model_dump(by_alias=True)
                            self.assertIs(tool["annotations"]["readOnlyHint"], True)
                            self.assertIs(tool["inputSchema"]["additionalProperties"], False)
                            if method == "unichem_map_compound":
                                self.assertIn("allOf", tool["inputSchema"])
                            result = (await session.call_tool(method, arguments)).model_dump(by_alias=True)
                            self.assertIs(result["isError"], False)
                            data = result["structuredContent"]
                            self.assertIsInstance(data, dict)
                            if method == "pdbe_get_validation":
                                self.assertIs(data["entry_found"], True)
                                self.assertEqual(data["entry"], {"title": "Synthetic entry", "experimental_method": ["Solution NMR"]})
                                self.assertIsNone(data["quality_scores"]["data_quality"])
                                self.assertEqual(data["quality_scores"]["experiment_data_available"], "unknown")
                                self.assertEqual(data["validation_stats"], {"rna_suite": {"num_checked": 0, "num_outliers": 0, "percent_outliers": None}})
                            else:
                                self.assertEqual(data["records"][0]["standard_inchi_key"], key)
                                self.assertEqual(data["records"][0]["matching_mappings"], 2)
                            before = len(calls)
                            rejected = (await session.call_tool(method, {**arguments, "url": "https://foreign.invalid"})).model_dump(by_alias=True)
                            self.assertIs(rejected["isError"], True)
                            if method == "unichem_map_compound":
                                rejected = (await session.call_tool(method, {"id_type": "inchikey", "identifier": "CHEMBL1"})).model_dump(by_alias=True)
                                self.assertIs(rejected["isError"], True)
                            self.assertEqual(len(calls), before)
            self.assertCountEqual([path for method, path, _ in calls if method == "GET"], [
                "/pdbe/api/pdb/entry/summary/1cbs",
                "/pdbe/api/validation/summary_quality_scores/entry/1cbs",
                "/pdbe/api/validation/key_validation_stats/entry/1cbs",
            ])
            self.assertEqual([call for call in calls if call[0] == "POST"], [
                ("POST", "/unichem/api/v1/compounds", {"compound": "CHEMBL1", "type": "sourceID", "sourceID": 1})])
        finally:
            upstream.shutdown()
            upstream.server_close()
            worker.join(timeout=2)
            self.assertFalse(worker.is_alive())


if __name__ == "__main__":
    unittest.main()
