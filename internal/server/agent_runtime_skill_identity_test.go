package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
	"synon-go/internal/skills"
)

func TestPendingSkillDoesNotRewriteRequestedIdentity(t *testing.T) {
	run := &sessionRunnerChatRun{}
	run.setPendingRequiredSkillNames("foundation")
	gateway := serverAgentRuntimeToolGateway{taskRun: run}
	for _, requested := range []string{"workflow", "foundation", "unknown", ""} {
		t.Run(requested, func(t *testing.T) {
			input := map[string]any{"skill": requested, "args": "input.dat", "filter": "summary"}
			before := copyMapAny(input)
			actual := gateway.normalizeAdmittedToolArguments("skill", input)
			if !reflect.DeepEqual(actual, before) || !reflect.DeepEqual(input, before) {
				t.Fatalf("pending inspection substituted a different request: before=%#v admitted=%#v source=%#v", before, actual, input)
			}
		})
	}
}

func TestSkillGatewayPendingInspectionRecoversWithoutSubstitution(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	catalog := skills.NewCatalog()
	catalog.AddSkill(skills.Skill{Name: "foundation", Path: "builtin:foundation", Body: "FOUNDATION CONTRACT"})
	catalog.AddSkill(skills.Skill{Name: "workflow", Path: "builtin:workflow", Body: "WORKFLOW CONTRACT"})
	fixture.server.skillCatalog = catalog
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	gateway := serverAgentRuntimeToolGateway{
		server: fixture.server, taskRun: run, allowedTools: []string{"skill"},
	}
	messages := []agentruntime.Message{
		{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "proposal", Name: "ask_user"}}},
		{Role: "tool", ToolCallID: "proposal", Content: `{"executed":false,"status":"implementation_decision_contract_incomplete","required_skills":["foundation"]}`},
	}
	tools := []agentruntime.ToolSchema{{Name: "skill"}, {Name: "ask_user"}}
	if choice := gateway.RequiredToolChoice(messages, tools); choice == nil {
		t.Fatal("fixture has no pending contract inspection")
	}
	call := agentruntime.ToolCall{ID: "requested-workflow", Name: "skill", Arguments: json.RawMessage(`{"skill":"workflow","args":"input.dat"}`)}
	blocked, err := gateway.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	if got := mapValue(blocked.Value); got["status"] != "required_skill_load_pending" || got["executed"] != false {
		t.Fatalf("a different contract was silently returned for the requested identity: %#v", blocked.Value)
	}
	if got := run.executedSkillNamesSnapshot(); len(got) != 0 {
		t.Fatalf("rejected request loaded another contract: %#v", got)
	}
	load := agentruntime.ToolCall{ID: "load-required", Name: "skill", Arguments: json.RawMessage(`{"skill":"foundation"}`)}
	result, err := gateway.Execute(ctx, load)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := result.Value.(string)
	if !ok || !strings.Contains(text, "FOUNDATION CONTRACT") {
		t.Fatalf("exact required load failed: %#v", result.Value)
	}
	encoded, err := json.Marshal(result.Value)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{load}},
		agentruntime.Message{Role: "tool", ToolCallID: load.ID, Content: string(encoded)})
	if choice := gateway.RequiredToolChoice(messages, tools); choice != nil {
		t.Fatalf("actual loaded receipt did not release pending inspection: %#v", choice)
	}
	call.ID = "retry-requested-workflow"
	result, err = gateway.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	text, ok = result.Value.(string)
	name, found := providerSkillResultName(text)
	if !ok || !found || name != "workflow" || !strings.Contains(text, "WORKFLOW CONTRACT") || strings.Contains(text, "FOUNDATION CONTRACT") {
		t.Fatalf("recovered request did not load its exact contract: %#v", result.Value)
	}
}
