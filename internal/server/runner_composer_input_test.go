package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Exercise the accepted canonical input through the real runner, rather than
// manufacturing the legacy session.Orchestration that the browser never writes.
func TestRunnerCanonicalComposerSelectionReachesInitialSkillContext(t *testing.T) {
	app, frame := boundPersonalSkillFixture(t)
	attachPersonalSkillFixture(t, app)
	repo, err := app.workspaceStore.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app.transcriptStore = repo
	var materialized []bool
	var mu sync.Mutex
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(request.Messages)
		mu.Lock()
		materialized = append(materialized, strings.Contains(string(raw), "Read existing files and report differences."))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Fixture review complete."},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	for index, selected := range []bool{false, true, false} {
		id := []string{"initial", "selected-followup", "cleared-followup"}[index]
		inputData := map[string]any{"request": "Review the existing fixture evidence."}
		if selected {
			inputData["inject_skills"] = []string{boundPersonalFixtureSkill}
		}
		if _, _, err := app.submitFrameMessage(app.workspaceStore, frameMessageSubmission{
			FrameID: frame.ID, MessageUUID: id, ClientMessageID: id,
			Text: "Review the existing fixture evidence.", InputData: inputData,
		}); err != nil {
			t.Fatal(err)
		}
		result, err := app.RunSessionRunnerChatOnce(t.Context(), SessionRunnerChatOptions{
			SessionID: frame.ID, RunnerID: "fixture-" + id,
			Endpoint: provider.URL, Model: "fixture-model", APIKey: "fixture-key",
			MaxAttempts: 1, LeaseTTL: time.Minute, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
		})
		mu.Lock()
		observed := append([]bool(nil), materialized...)
		mu.Unlock()
		if err != nil || result.Status != "completed" || len(observed) != index+1 {
			t.Fatalf("turn=%s result=%+v requests=%d err=%v", id, result, len(observed), err)
		}
		if observed[index] != selected {
			t.Fatalf("turn=%s initial materialized Skill=%t, explicitly selected=%t", id, observed[index], selected)
		}
	}
}

func TestRunnerCanonicalComposerMCPFenceUsesClaimedInput(t *testing.T) {
	app, frame := boundPersonalSkillFixture(t)
	repo, err := app.workspaceStore.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app.transcriptStore = repo
	if _, _, err := app.submitFrameMessage(app.workspaceStore, frameMessageSubmission{
		FrameID: frame.ID, MessageUUID: "selected-mcp", ClientMessageID: "selected-mcp", Text: "Review selected inputs.",
		InputData: map[string]any{"inject_mcp_server_ids": []string{"selected-connector"}, "files": []string{"selected.csv"}},
	}); err != nil {
		t.Fatal(err)
	}
	authority, claimed, _, err := app.claimTranscriptFrameRunner(t.Context(), frame.ID, "fixture-mcp", time.Minute, transcriptstore.ResumeSourceFresh, 0)
	if err != nil || !claimed {
		t.Fatalf("claimed=%t err=%v", claimed, err)
	}
	ctx := withTranscriptRunnerChatRun(t.Context(), &sessionRunnerChatRun{SessionID: frame.ID, Transcript: authority})
	if _, _, err := app.submitFrameMessage(app.workspaceStore, frameMessageSubmission{
		FrameID: frame.ID, MessageUUID: "newer-mcp", ClientMessageID: "newer-mcp", Text: "Later input.",
		InputData: map[string]any{"inject_mcp_server_ids": []string{"newer-connector"}},
	}); err != nil {
		t.Fatal(err)
	}
	selected, constrained, err := app.webAssistantRuntimeMCPSelection(ctx, "local", frame.ID)
	if err != nil || !constrained || !slices.Equal(selected, []string{"selected-connector"}) {
		t.Fatalf("claimed MCP fence=%v constrained=%t err=%v", selected, constrained, err)
	}
	legacy := sessionstore.Session{ID: frame.ID, Orchestration: map[string]any{"inputData": map[string]any{"files": []string{"stale.csv"}}}}
	projected, err := app.projectClaimedRunnerInputData(ctx, legacy, authority)
	if err != nil || !strings.Contains(appendWebComposerRuntimeContext("", projected.Orchestration["inputData"].(map[string]any)), "selected.csv") {
		t.Fatalf("claimed file projection err=%v", err)
	}
	if !strings.Contains(appendWebComposerRuntimeContext("", legacy.Orchestration["inputData"].(map[string]any)), "stale.csv") {
		t.Fatal("runtime projection mutated the compatibility session")
	}
	if selected, _, err := app.webAssistantRuntimeMCPSelection(context.Background(), "local", frame.ID); err != nil || len(selected) != 0 {
		t.Fatalf("unclaimed catalog borrowed an active/queued selection: %v %v", selected, err)
	}
	forged := *authority
	forged.Claim.OwnerID = "other-owner"
	badCtx := withTranscriptRunnerChatRun(t.Context(), &sessionRunnerChatRun{SessionID: frame.ID, Transcript: &forged})
	if _, _, err := app.webAssistantRuntimeMCPSelection(badCtx, "local", frame.ID); err == nil {
		t.Fatal("foreign claim acquired the MCP selection")
	}
}

func TestRunnerCanonicalComposerContextBindingIsIdempotent(t *testing.T) {
	data := map[string]any{"inject_skills": []string{"fixture-review"}, "files": []string{"fixture.csv"}}
	first := appendWebComposerRuntimeContext("base", data)
	if got := appendWebComposerRuntimeContext(first, data); got != first {
		t.Fatal("repeated model/tool authority binding duplicated turn-scoped context")
	}
}
