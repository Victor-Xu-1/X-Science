package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	runtimekv "synon-go/internal/persistence/runtimekv"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/providers"
)

func TestWebConversationCloneCompletedReplyBoundary(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	artifact := saveReplyBranchArtifact(t, f, "branch-first", "first-result.txt")
	finishRoundPresentation(t, f, "completed")
	first := f.claim.Attempt
	startNextPresentationRound(t, f)
	saveReplyBranchArtifact(t, f, "branch-later", "later-result.txt")
	finishRoundPresentation(t, f, "completed")
	path := "/api/conversations/" + f.stream.FrameID + "/messages?limit=80"
	before := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	if before.Code != http.StatusOK {
		_, _, cause := f.server.loadActivatedTranscriptWebHistoryView(context.Background(), f.stream.OwnerID, f.stream.FrameID, "")
		for nested := cause; nested != nil; nested = errors.Unwrap(nested) {
			t.Logf("source cause: %T %+v", nested, nested)
		}
		t.Fatalf("source fixture unreadable: %d %s", before.Code, before.Body.String())
	}
	body := map[string]any{
		"conversation": map[string]any{"id": f.stream.FrameID},
		"intent_id":    "e0f2df86-19c4-4486-92e9-6d9a1f055e7e", "through_attempt": first,
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", body, f.stream.OwnerID)
	if response.Code != http.StatusCreated {
		source, _, _ := f.store.GetCompatibilityFrame(f.stream.FrameID)
		_, err := f.store.CloneConversationWithTranscript(context.Background(), workspace.CloneConversationInput{
			OwnerUserID: f.stream.OwnerID, SourceFrameID: f.stream.FrameID, ExpectedSourceIncarnationID: source.IncarnationID,
			TargetFrameID: "debug-reply-branch", TargetName: "branch", ThroughAttempt: first,
		})
		t.Fatalf("branch %d: %s cause=%v", response.Code, response.Body.String(), err)
	}
	id := webString(p3DecodeObject(t, response)["id"])
	page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+id+"/messages?limit=80", nil, f.stream.OwnerID)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "Next round") {
		_, _, cause := f.server.loadActivatedTranscriptWebHistoryView(context.Background(), f.stream.OwnerID, id, "")
		for nested := cause; nested != nil; nested = errors.Unwrap(nested) {
			t.Logf("history cause: %T %+v", nested, nested)
		}
		t.Fatalf("branch included later round: %d %s cause=%v", page.Code, page.Body.String(), cause)
	}
	if !strings.Contains(page.Body.String(), webString(artifact["version_id"])) || strings.Contains(page.Body.String(), "later-result.txt") {
		t.Fatalf("artifact boundary lost: %s", page.Body.String())
	}
	again := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", body, f.stream.OwnerID)
	if again.Code != http.StatusCreated || webString(p3DecodeObject(t, again)["id"]) != id {
		t.Fatalf("retry: %s", again.Body.String())
	}
	after := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	if before.Body.String() != after.Body.String() {
		t.Fatal("source changed")
	}
	stream, found, err := f.repo.GetFrameStreamBySession(context.Background(), f.stream.OwnerID, id)
	if err != nil || !found || stream.InputRevision != 1 || stream.ConsumedInputRevision != 1 {
		t.Fatalf("branch counters: %+v %v", stream, err)
	}
	var running int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_attempts WHERE stream_uid=? AND status='running'`, stream.UID).Scan(&running); err != nil || running != 0 {
		t.Fatal("branch started runner", err)
	}
	for _, tc := range []struct {
		name   string
		value  any
		owner  string
		status int
	}{
		{"negative", -1, f.stream.OwnerID, http.StatusBadRequest},
		{"missing", 99999, f.stream.OwnerID, http.StatusConflict},
		{"foreign", first, "other-owner", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := map[string]any{"conversation": map[string]any{"id": f.stream.FrameID}, "through_attempt": tc.value}
			result := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", invalid, tc.owner)
			if result.Code != tc.status {
				t.Fatalf("got %d %s", result.Code, result.Body.String())
			}
		})
	}
	// The retained prefix is valid runtime context and accepts independent input,
	// but copying it alone did not dispatch or charge for any new model call.
	if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "branch-next", FrameEventID: "branch-next-event", MessageUUID: "branch-next-message", Text: "Independent next step",
	}); err != nil {
		t.Fatal(err)
	}
	if source := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID); source.Body.String() != before.Body.String() {
		t.Fatal("independent branch input changed source")
	}
}

func saveReplyBranchArtifact(t *testing.T, f *agentSaveArtifactsFixture, call, path string) map[string]any {
	t.Helper()
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, "controlled branch fixture "+call)
	f.saveExecution(t, f.identity.access, f.projectPath, "execution-"+call, int(f.claim.Attempt), write)
	input := map[string]any{"files": []any{path}, "destination": map[string]any{path: "snapshot"}, "language": "text", "human_description": "Save branch result"}
	raw, _ := json.Marshal(map[string]any{"toolCallId": call, "toolName": "save_artifacts", "toolPhase": "start", "status": "running", "toolInput": input})
	_, source, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: call + "-start", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withTranscriptArtifactRun(context.Background(), &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}, source.EventID)
	saved, err := f.server.executeAgentSaveArtifacts(ctx, f.identity, call, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]any{"toolCallId": call, "toolName": "save_artifacts", "toolPhase": "completed", "status": "completed", "toolResult": saved})
	if _, _, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: call + "-end", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
	}); err != nil {
		t.Fatal(err)
	}
	return agentSaveArtifactResults(t, saved)[0]
}

func TestWebReplyBranchInactivePrefixAndConcurrentRetry(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishRoundPresentation(t, f, "completed")
	first := f.claim.Attempt
	base, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := f.repo.ForkFrameUserMessageBranch(context.Background(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: base.ActiveBranchID,
		ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "sibling-edit", SourceClientMessageID: "save-user", SourceMessageIndex: 0, ReplacementText: "Sibling-only input", Destinations: []string{"ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "sibling-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("sibling runner: %+v %v", claim, err)
	}
	f.claim = claim.Claim
	body := map[string]any{"conversation": map[string]any{"id": f.stream.FrameID}, "intent_id": "0b78ca7d-4739-4fe7-9d8b-ae5c3f408af6", "through_attempt": first, "source_branch_id": base.ActiveBranchID}
	busy := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", body, f.stream.OwnerID)
	if busy.Code != http.StatusConflict {
		t.Fatalf("busy=%d %s", busy.Code, busy.Body.String())
	}
	finishRoundPresentation(t, f, "completed")
	results := make(chan *httptest.ResponseRecorder, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", body, f.stream.OwnerID)
		}()
	}
	group.Wait()
	close(results)
	id := ""
	for result := range results {
		if result.Code != http.StatusCreated {
			t.Fatalf("concurrent=%d %s", result.Code, result.Body.String())
		}
		got := webString(p3DecodeObject(t, result)["id"])
		if id != "" && id != got {
			t.Fatal("duplicate branch created")
		}
		id = got
	}
	page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+id+"/messages?limit=80", nil, f.stream.OwnerID)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "Sibling-only") || !strings.Contains(page.Body.String(), "Create scientific artifacts") {
		t.Fatalf("wrong branch: %s", page.Body.String())
	}
	startNextPresentationRound(t, f)
	finishRoundPresentation(t, f, "completed")
	retry := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", body, f.stream.OwnerID)
	if retry.Code != http.StatusCreated || webString(p3DecodeObject(t, retry)["id"]) != id {
		t.Fatalf("retry after source advanced: %d %s", retry.Code, retry.Body.String())
	}
	body["source_branch_id"] = fork.BranchID
	body["through_attempt"] = f.claim.Attempt
	conflict := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", body, f.stream.OwnerID)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed intent=%d %s", conflict.Code, conflict.Body.String())
	}
}

// Opt-in real browser integration: temporary SQLite, real HTTP API, production
// frontend components. Never connects to an existing user conversation.
func TestWebReplyBranchBrowser(t *testing.T) {
	if os.Getenv("SYNON_REPLY_BRANCH_BROWSER") != "1" {
		t.Skip("enable controlled browser integration")
	}
	f := newAgentSaveArtifactsFixture(t)
	f.server.runtimeStore = runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	artifact := saveReplyBranchArtifact(t, f, "browser-branch", "branch-result.txt")
	if err := f.store.PublishArtifactVersion(context.Background(), webString(artifact["version_id"]), webString(artifact["artifact_id"]), f.stream.ProjectID, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	f.server.recordSessionRunnerModelAudit(f.stream.SessionID, int(f.claim.Attempt), providers.AuditRecord{
		Protocol: providers.ProtocolOpenAICompatible, Model: "controlled-audit-model", HTTPStatus: 200,
		PromptTokens: 100, CacheReadTokens: 80, CompletionTokens: 10, TotalTokens: 110,
	})
	finishRoundPresentation(t, f, "completed")
	first := f.claim.Attempt
	startNextPresentationRound(t, f)
	finishRoundPresentation(t, f, "completed")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Synon-User-Id", f.stream.OwnerID)
		f.server.Handler().ServeHTTP(w, r)
	}))
	defer server.Close()
	frontend, err := filepath.Abs("../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", "tests/web-e2e/replyBranch.browser.mjs")
	command.Dir = frontend
	data, _ := json.Marshal(map[string]any{"source": f.stream.FrameID, "attempt": first, "artifact": artifact})
	command.Env = append(os.Environ(), "SYNON_BRANCH_API="+server.URL, "SYNON_BRANCH_FIXTURE="+string(data))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	fmt.Println(string(output))
}
