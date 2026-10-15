package server

import (
	"context"
	"testing"

	"x-science/internal/toolgateway"
)

func TestGatewaySemanticReviewDoesNotForceMCPExecution(t *testing.T) {
	run := &sessionRunnerChatRun{
		CorrectionReason: "completion_review_correction_required",
		CorrectionDetail: "explicit user-required tool evidence is incomplete; the explicitly requested real MCP method execution has no completed MCP tool result",
	}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	for _, code := range []string{
		`print("inspect the existing result")`,
		`servers = host.mcp.list_servers()`,
		`result = host.mcp("test_connector", "query", value="example")`,
	} {
		t.Run(code, func(t *testing.T) {
			invocation := toolgateway.NewInvocation(ctx, "test-call", "repl", nil,
				&serverAgentRuntimeGatewayExecution{gateway: serverAgentRuntimeToolGateway{server: &Server{}, taskRun: run}})
			invocation.CanonicalName = "repl"
			invocation.Input = map[string]any{"code": code}
			serverAgentRuntimeGatewayPreflight(invocation)
			if invocation.ContinuationState() != toolgateway.Continue {
				t.Fatalf("semantic review changed preflight admission: %#v", invocation.Value)
			}
			// Only the preflight stage runs. No MCP request or code is executed.
		})
	}
}
