package server

// The compact parameter rows are a convenience view, not a schema authority.
// Filtered discovery carries the exact admitted schema through the existing
// managed host-result transport, including its large-result streaming path.
func kernelMCPCatalogInputSchemaFields(inputSchema map[string]any) map[string]any {
	status := "complete"
	if inputSchema == nil {
		status = "unavailable"
	}
	return map[string]any{
		"input_schema":        inputSchema,
		"input_schema_status": status,
	}
}
