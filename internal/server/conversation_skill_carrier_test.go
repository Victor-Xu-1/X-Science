package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestConversationSkillAuthoringWholeJSONCarrierInstalls(t *testing.T) {
	arguments := conversationSkillDraftArgumentsForTest(t, nil)
	fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{{
		message: map[string]any{"role": "assistant", "content": arguments},
	}})
	before, err := fixture.server.loadConversationSkillMessages(context.Background(), "local", fixture.frameID)
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.save(context.Background())
	if response.Code != http.StatusCreated {
		t.Fatalf("whole-object JSON save status=%d want201", response.Code)
	}
	if requests := fixture.capturedRequests(); len(requests) != 1 {
		t.Fatalf("valid JSON draft was needlessly regenerated: requests=%d want1", len(requests))
	} else {
		assertConversationSkillDraftRequest(t, requests[0])
	}
	listed := compatJSONArrayRequest(t, fixture.server.Handler(), http.MethodGet, "/api/skills", "local", nil, http.StatusOK)
	found := false
	for _, entry := range listed {
		if entry["name"] == "typed-fixture-workflow" {
			found = entry["source"] == "custom" && entry["is_custom"] == true
		}
	}
	if !found {
		t.Fatal("validated JSON draft is missing from the personal Skill list")
	}
	loaded, err := fixture.server.executeSkillTool(map[string]any{"skill": "typed-fixture-workflow"})
	if err != nil {
		t.Fatal(err)
	}
	data := loaded.(map[string]any)["data"].(map[string]any)
	if data["loaded"] != true || data["executed"] != false {
		t.Fatal("loading a saved Skill must not pretend its helper executed")
	}
	restarted := New(Options{Workspace: fixture.server.workspaceStore, FileRoot: fixture.root})
	if _, found := findCatalogSkill(restarted.skillCatalog, "typed-fixture-workflow"); !found {
		t.Fatal("saved JSON draft disappeared after catalog reconstruction")
	}
	after, err := fixture.server.loadConversationSkillMessages(context.Background(), "local", fixture.frameID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("saving/loading a JSON draft changed the original conversation")
	}
}

func TestConversationSkillAuthoringWholeJSONCarrierRejectsAmbiguity(t *testing.T) {
	valid := conversationSkillDraftArgumentsForTest(t, nil)
	cases := map[string]agentruntime.Message{
		"prose":                {Content: "Here is the draft:\n" + valid},
		"fence":                {Content: "```json\n" + valid + "\n```"},
		"trailing":             {Content: valid + "\n{}"},
		"duplicate":            {Content: "{\"name\":\"other\"," + valid[1:]},
		"missing_field":        {Content: `{"name":"fixture","description":"Fixture","skill_markdown":"Body"}`},
		"wrong_tool_with_json": {Content: valid, ToolCalls: []agentruntime.ToolCall{{Name: "other_return", Arguments: []byte(valid)}}},
		"multiple_tools_with_json": {Content: valid, ToolCalls: []agentruntime.ToolCall{
			{Name: conversationSkillDraftReturnTool, Arguments: []byte(valid)},
			{Name: conversationSkillDraftReturnTool, Arguments: []byte(valid)},
		}},
	}
	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := conversationSkillDraftFromResponse(agentruntime.ModelResponse{Message: message}, ""); err == nil {
				t.Fatal("ambiguous or unvalidated JSON was accepted")
			}
		})
	}
}

func TestConversationSkillAuthoringWholeJSONCarrierDoesNotAcceptRefusalOrTruncation(t *testing.T) {
	for _, reason := range []string{"content_filter", "length"} {
		t.Run(reason, func(t *testing.T) {
			fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{{
				stopReason: reason,
				message:    map[string]any{"role": "assistant", "content": conversationSkillDraftArgumentsForTest(t, nil)},
			}})
			response := fixture.save(context.Background())
			if response.Code != http.StatusBadGateway || len(fixture.capturedRequests()) != 1 {
				t.Fatal("refused/truncated content was installed or entered a format retry")
			}
			fixture.assertNoInstallation(t)
		})
	}
}

func TestConversationSkillAuthoringCarrierDiagnosticIsMetadataOnly(t *testing.T) {
	const marker = "synthetic-private-marker"
	response := agentruntime.ModelResponse{Message: agentruntime.Message{
		Content: marker,
		ToolCalls: []agentruntime.ToolCall{{
			Name: marker, Arguments: []byte(marker), ProviderProtocolDiagnostic: marker,
		}},
	}}
	cause := errors.New(marker)
	failure := invalidConversationSkillDraft(skillDraftManifestContract, cause)
	event := conversationSkillDraftDiagnostic(response, 2, failure)
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), marker) ||
		event.ValidationReason != skillDraftManifestContract || event.ReturnCarrier != "tool_call" ||
		event.Stage != "draft_validation" || event.Code != skillAuthoringInvalidDraft ||
		event.ToolCallCount != 1 || event.ContentBytes != len(marker) || event.ArgumentBytes != len(marker) {
		t.Fatal("draft diagnostic either leaked raw content or lost closed metadata")
	}
	if !errors.Is(failure, cause) || strings.Contains(failure.Error(), marker) {
		t.Fatal("validation error must retain cause identity without displaying it")
	}
	valid := agentruntime.ModelResponse{Message: agentruntime.Message{Content: conversationSkillDraftArgumentsForTest(t, nil)}}
	if _, err := conversationSkillDraftFromResponse(valid, ""); err != nil {
		t.Fatal(err)
	}
	success := conversationSkillDraftDiagnostic(valid, 1, nil)
	if success.Stage != "draft_validated" || success.ReturnCarrier != "whole_json" || success.Code != "" || success.ValidationReason != "" {
		t.Fatal("validated JSON carrier was not recorded distinctly from a failed draft")
	}
}

func TestConversationSkillAuthoringCarrierValidationReasons(t *testing.T) {
	valid := conversationSkillDraftArgumentsForTest(t, nil)
	invalidManifest := conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) {
		fields["skill_markdown"] = "---\nname: [unterminated\n---\nFixture body."
	})
	cases := []struct {
		name    string
		message agentruntime.Message
		reason  conversationSkillDraftReason
	}{
		{"empty", agentruntime.Message{}, skillDraftMissingReturn},
		{"json", agentruntime.Message{Content: "not JSON"}, skillDraftJSONContract},
		{"manifest", agentruntime.Message{Content: invalidManifest}, skillDraftManifestContract},
		{"wrong_tool", agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{Name: "other_return"}}}, skillDraftUnexpectedReturn},
		{"protocol", agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{Name: conversationSkillDraftReturnTool,
			Arguments: []byte(valid), ProviderProtocolDiagnostic: "synthetic-private-marker"}}}, skillDraftProviderArguments},
		{"ambiguous", agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{}, {}}}, skillDraftAmbiguousReturn},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := agentruntime.ModelResponse{Message: test.message}
			_, err := conversationSkillDraftFromResponse(response, "")
			if err == nil || conversationSkillDraftDiagnostic(response, 1, err).ValidationReason != test.reason {
				t.Fatal("invalid draft lost its closed structural reason")
			}
		})
	}
}
