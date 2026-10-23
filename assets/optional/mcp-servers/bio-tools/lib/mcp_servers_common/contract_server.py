"""Declared first-party input schemas on the existing public MCP SDK path."""
from __future__ import annotations

import copy
from typing import Any

from jsonschema import Draft7Validator
from mcp.types import CallToolResult, TextContent

from .mcp_compat import MCPServer


class ContractMCPServer(MCPServer):
    """Keep discovery and direct-call validation on one declared input contract.

    Only the public list_tools/call_tool extension points are overridden. The
    SDK continues owning registration, execution, output conversion, context,
    transport and errors. Existing unoverridden tools delegate unchanged; the
    serving gate still owns deferred-tool removal through its public APIs.
    """

    def __init__(self, *args, input_schemas: dict[str, dict], **kwargs):
        self._declared_input_schemas = copy.deepcopy(input_schemas)
        self._declared_input_validators = {}
        for name, schema in self._declared_input_schemas.items():
            if not isinstance(name, str) or not name:
                raise ValueError("Declared tool name must be non-empty")
            Draft7Validator.check_schema(schema)
            self._declared_input_validators[name] = Draft7Validator(schema)
        super().__init__(*args, **kwargs)

    async def list_tools(self):
        tools = await super().list_tools()
        result = []
        for tool in tools:
            schema = self._declared_input_schemas.get(tool.name)
            if schema is None:
                result.append(tool)
            else:
                wire = tool.model_dump(by_alias=True)
                wire["inputSchema"] = copy.deepcopy(schema)
                result.append(type(tool).model_validate(wire))
        return result

    async def call_tool(self, name: str, arguments: dict[str, Any], *args, **kwargs):
        validator = self._declared_input_validators.get(name)
        if validator is not None and next(validator.iter_errors(arguments), None) is not None:
            # Do not expose validation values, paths or provider/request data.
            return CallToolResult(content=[TextContent(type="text", text="Tool arguments do not match the declared input schema")], isError=True)
        return await super().call_tool(name, arguments, *args, **kwargs)
