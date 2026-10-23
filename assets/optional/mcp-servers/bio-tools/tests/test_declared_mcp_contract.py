"""Generic public SDK schema projection/validation; no target-specific behavior."""
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "lib"))
from mcp_servers_common.contract_server import ContractMCPServer

SCHEMA = {
    "type": "object", "additionalProperties": False,
    "properties": {"kind": {"enum": ["record", "integer"]}, "identifier": {"type": "string"}},
    "required": ["kind", "identifier"],
    "allOf": [{"if": {"properties": {"kind": {"const": "record"}}},
               "then": {"properties": {"identifier": {"pattern": "^REC-[0-9]{4}$"}}},
               "else": {"properties": {"identifier": {"pattern": "^[1-9][0-9]*$"}}}}],
}


class DeclaredMCPContracts(unittest.IsolatedAsyncioTestCase):
    def server(self):
        calls = []
        source = {"resolve": SCHEMA}
        server = ContractMCPServer("test-declared-contract", input_schemas=source)
        @server.tool()
        async def resolve(kind: str, identifier: str) -> dict:
            calls.append((kind, identifier))
            return {"kind": kind, "identifier": identifier}
        @server.tool()
        async def unchanged(value: int) -> dict:
            return {"value": value}
        return server, calls, source

    async def test_discovery_projects_exact_schema_and_has_no_shared_mutable_copy(self):
        server, _, source = self.server()
        source["resolve"] = {}
        first = {tool.name: tool.model_dump(by_alias=True) for tool in await server.list_tools()}
        self.assertEqual(first["resolve"]["inputSchema"], SCHEMA)
        self.assertNotIn("allOf", first["unchanged"]["inputSchema"])
        first["resolve"]["inputSchema"]["properties"]["kind"]["enum"].append("foreign")
        second = {tool.name: tool.model_dump(by_alias=True) for tool in await server.list_tools()}
        self.assertEqual(second["resolve"]["inputSchema"], SCHEMA)

    async def test_conditional_and_unknown_fields_reject_before_real_tool_call(self):
        server, calls, _ = self.server()
        for arguments in ({"kind": "integer", "identifier": "REC-0042"},
                          {"kind": "record", "identifier": "42"},
                          {"kind": "record", "identifier": "REC-0042", "url": "sensitive sentinel"}):
            result = (await server.call_tool("resolve", arguments)).model_dump(by_alias=True)
            self.assertIs(result["isError"], True)
            self.assertNotIn("sensitive sentinel", str(result))
        self.assertEqual(calls, [])
        await server.call_tool("resolve", {"kind": "record", "identifier": "REC-0042"})
        await server.call_tool("resolve", {"kind": "integer", "identifier": "42"})
        self.assertEqual(calls, [("record", "REC-0042"), ("integer", "42")])
        await server.call_tool("unchanged", {"value": 3})

    async def test_public_remove_gate_cannot_be_undone_by_schema_projection(self):
        server, calls, _ = self.server()
        server.remove_tool("resolve")
        self.assertNotIn("resolve", {tool.name for tool in await server.list_tools()})
        with self.assertRaises(Exception):
            await server.call_tool("resolve", {"kind": "record", "identifier": "REC-0042"})
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
