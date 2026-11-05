package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"x-science/internal/agentruntime"
)

type closedCorrectionTextToolModel struct{ request agentruntime.ModelRequest }

func (model *closedCorrectionTextToolModel) Complete(_ context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.request = request
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant",
		Content: "<tool_call><function=edit_file><parameter=content>SYNTHETIC</parameter></function></tool_call>"}}, nil
}

func TestCorrectionRevalidationRejectsClosedRoundTextToolEnvelope(t *testing.T) {
	run := &sessionRunnerChatRun{TaskIntent: "finish the current authorized work",
		CorrectionReason: "artifact_reference_correction_required", CorrectionDetail: "unresolved_template_marker:report.md"}
	messages := []agentruntime.Message{
		{Role: "user", Content: run.TaskIntent},
		{Role: "system", Content: sessionRunnerDurableCorrectionContextMarker},
		{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "saved-candidate", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["report.md"]}`)}}},
		{Role: "tool", ToolCallID: "saved-candidate", Content: `{"ok":true,"artifacts":[{"version_id":"changed-candidate","unchanged":false}]}`},
	}
	tools := []agentruntime.ToolSchema{{Name: "edit_file"}, {Name: "save_artifacts"}}
	gateway := serverAgentRuntimeToolGateway{taskRun: run}
	choice := gateway.RequiredToolChoice(messages, tools)
	if choice != "none" || !sessionRunnerCorrectionReadyForRevalidation(run, messages) {
		t.Fatal("fixture did not recover a changed candidate ready for immutable revalidation")
	}
	model := &closedCorrectionTextToolModel{}
	result, err := (agentruntime.Engine{Model: model, Tools: gateway}).Run(t.Context(), agentruntime.RunRequest{
		Messages: messages, Tools: tools, InitialToolChoice: choice, MaxToolRounds: 1,
	})
	var invalid *agentruntime.TextToolEnvelopePresentationError
	if !errors.As(err, &invalid) || result.FinalMessage.Content != "" || len(model.request.Tools) != 0 || model.request.ToolChoice != "none" {
		t.Fatalf("closed revalidation escaped the protocol boundary: error=%v provider_tools=%d final_bytes=%d", err, len(model.request.Tools), len(result.FinalMessage.Content))
	}
	kept := false
	for _, message := range result.Messages {
		kept = kept || message.Role == "tool" && message.ToolCallID == "saved-candidate"
	}
	if !kept {
		t.Fatal("closed revalidation discarded its existing save receipt")
	}
}
