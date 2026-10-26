package server

import (
	"context"
	"encoding/json"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

// Use real admitted control authority and the existing persisted-plan fixture.
// No tool, provider or new policy helper is executed by these observations.
func conversationControlFramePlanScopeFixture(t *testing.T, typed bool) (serverAgentRuntimeToolGateway, []agentruntime.ToolSchema) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	loadSkillControlCatalog(t, f)
	if typed {
		enabled := true
		if _, err := f.store.CreateAgent(workspace.CreateAgentInput{
			ID: "skill-control-agent", UserID: f.stream.OwnerID, Name: "OPERON", DisplayName: "Evidence assistant",
			Description: "Synthetic assistant for current-control plan scope", SystemPrompt: "PROFILE_PROMPT_MUST_SURVIVE",
			Unrestricted: true, Enabled: &enabled, SkillNames: []string{"customize", "skill-creator"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("original intent found=%t err=%v", found, err)
	}
	_, planStore, planFrame := newGeneratedPlanProcessFixture(t)
	plan, found, err := planStore.GetFrameRuntimeMetadata(planFrame)
	if err != nil || !found {
		t.Fatalf("plan fixture found=%t err=%v", found, err)
	}
	metadata, found, err := f.store.GetFrameRuntimeMetadata(f.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("frame metadata found=%t err=%v", found, err)
	}
	for key, value := range plan.ContextData {
		metadata.ContextData[key] = value
	}
	metadata.ContextData["_plan_task_intent_id"] = intent.ID
	metadata.ContextData["_plan_task_intent_revision"] = intent.Revision
	metadata.ContextData["_plan_task_intent_sha256"] = generatedPlanTaskIntentSHA(intent.Text)
	metadata.ContextData["_step_statuses"] = map[string]any{
		"module-1": map[string]any{
			"status": "in_progress", "title": "Mechanism",
			"research_continuation": map[string]any{
				"reason": "research_follow_up_required", "continuation_id": "prior-frontier",
				"research_session": map[string]any{"id": "prior-session", "mode": "continue"},
				"next_actions":     []any{map[string]any{"action": "search_more", "query": "prior-plan-follow-up"}},
			},
		},
	}
	if _, err := f.store.SetFrameRuntimeMetadata(f.stream.FrameID, metadata); err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt),
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if typed {
		finishSkillControlInput(t, f)
		submitSkillControlHTTPInput(t, f, "plan-scope-control", "CURRENT_CONTROL_SENTINEL", true)
		run = claimSkillControlInput(t, f)
	}
	_, control, err := f.server.prepareConversationSkillContinuation(ctx, SessionRunnerChatOptions{}, run)
	if err != nil || (control != nil) != typed {
		t.Fatalf("claimed current control=%#v typed=%t err=%v", control, typed, err)
	}
	run.currentControl = control
	text, id, revision := intent.Text, intent.ID, intent.Revision
	if control != nil {
		text = control.instruction()
		id, revision = control.taskIdentity(f.stream.UID)
	}
	if err := applySessionRunnerTaskIntent(run, text, intent.Language, id, revision); err != nil {
		t.Fatal(err)
	}
	session, err := f.server.loadTranscriptFrameSessionProjection(f.stream)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.server.restoreSessionRunnerPlanControl(session, run); err != nil {
		t.Fatal(err)
	}
	if matched := sessionRunnerPlanMatchesTask(metadata.ContextData, run); matched == typed {
		t.Fatalf("prior plan task match=%t typed=%t", matched, typed)
	}
	if available := run.planProgressAvailable.Load(); available == typed {
		t.Fatalf("restored plan progress=%t typed=%t", available, typed)
	}
	schemas := []agentruntime.ToolSchema{{Name: "skill"}, {Name: updateStepStatusToolName},
		{Name: "web_search", Capabilities: []string{"search", "source-evidence"}}}
	run.setToolCapabilityCatalog(schemas)
	return serverAgentRuntimeToolGateway{server: f.server, sessionID: f.stream.FrameID,
		taskRun: run, toolSchemas: schemas}, schemas
}

func TestConversationControlDoesNotInheritFramePlanToolChoice(t *testing.T) {
	for _, row := range []struct {
		name  string
		typed bool
	}{{"ordinary", false}, {"save-control", true}} {
		t.Run(row.name, func(t *testing.T) {
			gateway, schemas := conversationControlFramePlanScopeFixture(t, row.typed)
			messages := []agentruntime.Message{{Role: "user", Content: gateway.taskRun.TaskIntent}}
			for _, producer := range []struct {
				name string
				read func() any
			}{
				{"priority", func() any { return gateway.generatedPlanPriorityToolChoice(messages, schemas) }},
				{"engine-policy", func() any { return gateway.RequiredToolChoice(messages, schemas) }},
				{"initial-policy", func() any {
					return sessionRunnerStatefulInitialToolChoice(agentruntime.Engine{Tools: gateway}, messages, schemas)
				}},
			} {
				t.Run(producer.name, func(t *testing.T) {
					choice := producer.read()
					if row.typed {
						if choice != nil {
							t.Errorf("unrelated frame plan constrained current control: %#v", choice)
						}
					} else if mapValue(choice)["name"] != "web_search" {
						t.Errorf("ordinary source continuation lost its policy: %#v", choice)
					}
				})
			}
		})
	}
	// Green-only: a matching control-owned plan and nil-run legacy routing remain usable.
	t.Run("matching-control-plan-green-only", func(t *testing.T) {
		gateway, schemas := conversationControlFramePlanScopeFixture(t, true)
		legacy := gateway
		legacy.taskRun = nil
		if choice := mapValue(legacy.generatedPlanPriorityToolChoice(nil, schemas)); choice["name"] != "web_search" {
			t.Fatalf("nil-run legacy source continuation changed: %#v", choice)
		}
		bindPlanToCurrentControlForTest(t, gateway)
		messages := []agentruntime.Message{{Role: "user", Content: gateway.taskRun.TaskIntent}}
		if choice := mapValue(gateway.RequiredToolChoice(messages, schemas)); choice["name"] != "web_search" {
			t.Errorf("matching current-control plan was disabled: %#v", choice)
		}
		if choice := mapValue(sessionRunnerStatefulInitialToolChoice(agentruntime.Engine{Tools: gateway}, messages, schemas)); choice["name"] != "web_search" {
			t.Errorf("matching control plan lost initial continuation: %#v", choice)
		}
	})

}

func TestConversationControlDoesNotInheritFramePlanToolInput(t *testing.T) {
	for _, row := range []struct {
		name  string
		typed bool
	}{{"ordinary", false}, {"save-control", true}} {
		t.Run(row.name, func(t *testing.T) {
			gateway, _ := conversationControlFramePlanScopeFixture(t, row.typed)
			for _, call := range []agentruntime.ToolCall{
				{ID: "step", Name: updateStepStatusToolName, Arguments: json.RawMessage(`{"step":"control-owned-step","status":"completed"}`)},
				{ID: "missing-query", Name: "web_search", Arguments: json.RawMessage(`{}`)},
				{ID: "explicit-query", Name: "web_search", Arguments: json.RawMessage(`{"query":"model-owned-query"}`)},
			} {
				t.Run(call.ID, func(t *testing.T) {
					resolved, valid := gateway.ToolCallExecutionIdentity(call)
					if !valid && row.typed && call.ID != "explicit-query" {
						return // A typed rejection also prevents cross-task injection.
					}
					var input map[string]any
					if !valid || json.Unmarshal(resolved.Arguments, &input) != nil {
						t.Fatalf("call identity unavailable: valid=%t args=%s", valid, resolved.Arguments)
					}
					switch call.ID {
					case "step":
						expected := "module-1"
						if row.typed {
							expected = "control-owned-step"
						}
						if input["step"] != expected {
							t.Errorf("plan rewrote step outside current task scope: %#v", input)
						}
					case "missing-query":
						if row.typed {
							if input["query"] != nil || input["research_session"] != nil {
								t.Errorf("plan injected an unrelated source continuation: %#v", input)
							}
						} else if input["query"] != "prior-plan-follow-up" {
							t.Errorf("ordinary continuation lost its missing locator: %#v", input)
						}
					case "explicit-query":
						if input["query"] != "model-owned-query" {
							t.Errorf("explicit model locator was replaced: %#v", input)
						}
					}
				})
			}
		})
	}
	// Green-only: filter current navigation, retain current source material and historical reads.
	t.Run("navigation-and-handoff-green-only", func(t *testing.T) {
		gateway, _ := conversationControlFramePlanScopeFixture(t, true)
		if gateway.server.generatedPlanDesiredOutputsContext(gateway.sessionID) == "" {
			t.Fatal("historical server-only plan read was removed")
		}
		if state := gateway.server.generatedPlanRuntimeNavigationContext(gateway.sessionID, gateway.taskRun); state != "" {
			t.Errorf("unrelated plan became current runtime navigation: %s", state)
		}
		call := agentruntime.ToolCall{ID: "current-source", Name: "web_search", Arguments: json.RawMessage(`{"query":"model-owned-query"}`)}
		result := map[string]any{"ok": true, "sources": []any{map[string]any{
			"title": "Current synthetic source", "url": "https://example.test/current",
			"record": map[string]any{"record_depth": "abstract_record", "abstract_complete": true,
				"abstract":        "The complete synthetic abstract records the current procedure and its limitations.",
				"citation_handle": "doi:10.1000/current", "citation_text": "Current source. DOI:10.1000/current."},
		}}}
		readHandoff := func(scoped bool) map[string]any {
			t.Helper()
			var state string
			if scoped {
				state = gateway.server.generatedPlanResearchHandoffContext(gateway.sessionID, call, result, gateway.taskRun)
			} else {
				state = gateway.server.generatedPlanResearchHandoffContext(gateway.sessionID, call, result)
			}
			var payload map[string]any
			if state == "" || json.Unmarshal([]byte(state), &payload) != nil {
				t.Fatalf("current source handoff missing: %q", state)
			}
			return payload
		}
		foreign := readHandoff(true)
		if len(anySliceValue(foreign["investigations"])) != 0 || stringValue(mapValue(foreign["source_call"])["id"]) != call.ID ||
			len(anySliceValue(mapValue(foreign["source_material"])["sources"])) != 1 {
			t.Errorf("scope filtering lost current source or retained old investigations: %#v", foreign)
		}
		if len(anySliceValue(readHandoff(false)["investigations"])) == 0 {
			t.Error("server-only historical handoff lost its original plan")
		}
		bindPlanToCurrentControlForTest(t, gateway)
		if gateway.server.generatedPlanRuntimeNavigationContext(gateway.sessionID, gateway.taskRun) == "" ||
			len(anySliceValue(readHandoff(true)["investigations"])) == 0 {
			t.Error("matching current-control navigation or handoff was disabled")
		}
	})

}

func bindPlanToCurrentControlForTest(t *testing.T, gateway serverAgentRuntimeToolGateway) {
	t.Helper()
	metadata, found, err := gateway.server.workspaceStore.GetFrameRuntimeMetadata(gateway.sessionID)
	if err != nil || !found {
		t.Fatalf("matching plan setup found=%t err=%v", found, err)
	}
	metadata.ContextData["_plan_task_intent_id"] = gateway.taskRun.TaskIntentID
	metadata.ContextData["_plan_task_intent_revision"] = gateway.taskRun.TaskIntentRevision
	metadata.ContextData["_plan_task_intent_sha256"] = generatedPlanTaskIntentSHA(gateway.taskRun.TaskIntent)
	if _, err := gateway.server.workspaceStore.SetFrameRuntimeMetadata(gateway.sessionID, metadata); err != nil {
		t.Fatal(err)
	}
}
