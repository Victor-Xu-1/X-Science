package server

import (
	"encoding/json"
	"testing"

	"x-science/internal/kernelcontract"
)

const largeMCPREPLArguments = `{"code":"import host"}`

func TestLargeMCPREPLFixtureInputContract(t *testing.T) {
	canonical, environment, err := kernelcontract.CanonicalInput(kernelcontract.ReplTool, json.RawMessage(largeMCPREPLArguments))
	if err != nil {
		t.Fatalf("large MCP fixture must create an admitted REPL operation: %v", err)
	}
	if string(canonical) != largeMCPREPLArguments || environment != kernelcontract.ReplTool {
		t.Fatalf("fixture canonical input/environment = %s / %q", canonical, environment)
	}
}
