package server

import (
	"context"
	"net/http"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestContextInspectorExplicitReadWithoutUsage(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	project := createP3Project(t, store, "inspect-empty", "local")
	created := p3JSONRequest(t, app, http.MethodPost, "/api/conversations", map[string]any{
		"name": "Inspector fixture", "assistant": map[string]any{"id": "xscience:OPERON"},
		"extra": map[string]any{"project_id": project.ID},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create HTTP %d: %s", created.Code, created.Body.String())
	}
	id := webString(p3DecodeObject(t, created)["id"])
	compact := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id+"/context-usage", nil, "")
	if _, found := p3DecodeObject(t, compact)["inspection"]; found {
		t.Fatal("compact ring read acquired inspection projection")
	}
	response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id+"/context-usage?inspect=1", nil, "")
	payload := p3DecodeObject(t, response)
	if response.Code != http.StatusOK || payload["status"] != "unavailable" {
		t.Fatalf("empty usage HTTP=%d payload=%+v", response.Code, payload)
	}
	if _, found := payload["inspection"]; !found {
		t.Fatal("explicit inspector request has no inspection field")
	}
	foreign := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id+"/context-usage?inspect=1", nil, "foreign-user")
	if foreign.Code != http.StatusNotFound && foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign inspector HTTP %d", foreign.Code)
	}
}

func TestContextInspectorLegacyCallsNeverInventPromptAssociation(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "inspect-project", "inspect-frame")
	if _, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:inspect-frame", OwnerID: "local", ExternalID: "inspect-frame", SessionID: "inspect-frame",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "inspect-project", FrameID: "inspect-frame", RootFrameID: "inspect-frame", Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	recorder := newSessionContextUsageRecorder(app, "inspect-frame", 1, SessionRunnerChatOptions{})
	request := recorder.begin("fixture-model", agentruntime.ModelRequest{})
	if request == nil {
		t.Fatal("fixture recorder did not begin")
	}
	recorder.finish(request, agentruntime.ModelResponse{}, nil)
	response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/inspect-frame/context-usage?inspect=1", nil, "")
	payload := p3DecodeObject(t, response)
	inspection, ok := payload["inspection"].(map[string]any)
	if response.Code != http.StatusOK || !ok {
		t.Fatalf("explicit inspector has no typed projection: HTTP=%d payload=%+v", response.Code, payload)
	}
	coverage, _ := inspection["coverage"].(map[string]any)
	if inspection["version"] != float64(1) || coverage["association"] != "unavailable" || coverage["unassociatedCallCount"] != float64(1) {
		t.Fatalf("legacy evidence invented association: %+v", inspection)
	}
	if turns, ok := inspection["turns"].([]any); !ok || len(turns) != 0 {
		t.Fatalf("request complete invented logical terminal: %+v", inspection)
	}
}

func TestContextInspectorClaimedInputAndCommittedTerminal(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	if recorder == nil || recorder.association == nil {
		t.Fatal("live claim did not capture an admitted input")
	}
	first := recorder.begin("model-a", agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "fixture"}}})
	if first == nil || first.Association.InputMessageID != "save-user-message" {
		t.Fatalf("rendered message identity lost: %+v", first)
	}
	if _, _, _, err := f.repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		ClientMessageID: "queued", FrameEventID: "queued-event", MessageUUID: "queued-message", Text: "Later input must not steal an in-flight call"}); err != nil {
		t.Fatal(err)
	}
	recorder.finish(first, agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
		Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}}}, nil)
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	calls := inspection["calls"].([]any)
	turns := inspection["turns"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["inputMessageId"] != "save-user-message" || len(turns) != 2 {
		t.Fatalf("queued input stole the captured call: %+v", inspection)
	}
	if turns[0].(map[string]any)["status"] != "running" || turns[1].(map[string]any)["status"] != "pending" {
		t.Fatalf("model completion invented runner terminal: %+v", turns)
	}
	terminal, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: f.claim, ClientMessageID: "inspect-finished", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)})
	if err != nil {
		t.Fatal(err)
	}
	inspection = readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	turn := inspection["turns"].([]any)[0].(map[string]any)
	if turn["status"] != "completed" || turn["terminalEventId"] != float64(terminal.EventID) || turn["completedAt"] == nil {
		t.Fatalf("committed terminal missing: %+v", turn)
	}
	if _, err := f.repo.ReadContextInspection(context.Background(), f.stream.UID, "foreign-owner"); err == nil {
		t.Fatal("foreign owner obtained prompt projection")
	}
	staleRun := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if stale := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, staleRun, SessionRunnerChatOptions{}); stale.association != nil {
		t.Fatal("completed claim reacquired live association")
	}
}

func readContextInspectorFixture(t *testing.T, app *Server, id, owner string) map[string]any {
	t.Helper()
	response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id+"/context-usage?inspect=1", nil, owner)
	inspection, ok := p3DecodeObject(t, response)["inspection"].(map[string]any)
	if response.Code != http.StatusOK || !ok {
		t.Fatalf("inspector HTTP=%d payload=%s", response.Code, response.Body.String())
	}
	return inspection
}
