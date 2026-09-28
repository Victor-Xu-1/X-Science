package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
	eventjournal "synon-go/internal/persistence/journal"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/tools/registry"
	"synon-go/internal/tools/webfetch"
	"synon-go/internal/tools/websearch"
)

type mixedReadProgressModel struct {
	requests  int
	url       string
	converge  bool
	readInput json.RawMessage
}

func (m *mixedReadProgressModel) Complete(_ context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	m.requests++
	if m.converge {
		for _, message := range request.Messages {
			if message.Role == "system" && strings.Contains(message.Content, "only idempotent receipts") {
				return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "Use the existing evidence."}}, nil
			}
		}
	}
	if m.requests > 8 {
		return agentruntime.ModelResponse{}, errors.New("test sentinel: unchanged read loop escaped progress detection")
	}
	remote, _ := json.Marshal(map[string]any{"url": m.url})
	readInput := m.readInput
	if len(readInput) == 0 {
		readInput = json.RawMessage(`{"file_path":"source.txt","offset":2,"limit":2000,"human_description":"Inspecting source tail"}`)
	}
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "Reading the remaining source again.", ToolCalls: []agentruntime.ToolCall{
		{ID: fmt.Sprintf("page-%d", m.requests), Name: "read_file", Arguments: readInput},
		{ID: fmt.Sprintf("fetch-%d", m.requests), Name: "web_fetch", Arguments: remote},
	}}}, nil
}

func TestReadProgressMixedNarrativeLoopWithRealFileHTTPAndJournal(t *testing.T) {
	for _, converge := range []bool{false, true} {
		t.Run(fmt.Sprintf("converge=%t", converge), func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.projectPath, "source.txt"), []byte("start\nfinal evidence\n"), 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write([]byte(strings.Repeat("original evidence ", 4000)))
			}))
			defer remote.Close()
			fixture.server.tools = registry.DefaultWithWebOptions(webfetch.Options{ClientForURL: func(context.Context, string) (*http.Client, error) { return remote.Client(), nil }}, websearch.Options{})
			run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), ClaimToken: fixture.claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
			ctx := withTranscriptRunnerChatRun(context.Background(), run)
			schemas := []agentruntime.ToolSchema{agentWorkspaceReadFileToolSchema(), {Name: "web_fetch", Capabilities: []string{"read-only", "idempotent-read"}}}
			gateway := serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, sessionID: run.SessionID, taskRun: run, toolSchemas: schemas, hasToolSnapshot: true, suppressHooks: true, fileReadLimitBytes: 8000}
			model := &mixedReadProgressModel{url: remote.URL, converge: converge}
			artifactID, _ := runnerLargeToolResultIdentities(fixture.stream, "seed-source", "source_reader")
			original, err := fixture.store.WriteRunnerLargeToolResult(ctx, workspace.WriteRunnerLargeToolResultInput{
				ArtifactID: artifactID, ProjectID: fixture.stream.ProjectID, RootFrameID: fixture.stream.RootFrameID, FrameID: fixture.stream.FrameID, StreamUID: fixture.stream.UID, OwnerUserID: fixture.stream.OwnerID,
				RunnerID: fixture.claim.RunnerID, ClaimToken: fixture.claim.ClaimToken, Attempt: fixture.claim.Attempt, SourceEventID: 1, ToolName: "source_reader", ToolCallID: "seed-source", Content: []byte(`{"content":"start\nfinal evidence\n"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			model.readInput, _ = json.Marshal(map[string]any{"version_id": original.VersionID, "json_pointer": "/content", "offset": 2, "limit": 2000, "human_description": "Reading immutable tail"})
			options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: fixture.claim.RunnerID}
			engine := agentruntime.Engine{Model: model, Tools: gateway, MaxToolResultBytes: 16000, LargeToolResults: runnerLargeToolResultAuthority{server: fixture.server}, OnEventError: func(event agentruntime.Event) error {
				if event.Type == agentruntime.EventModelResponse {
					return fixture.server.checkpointChatModelToolCalls(options, run, event.ToolCalls)
				}
				return fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, event)
			}}
			result, err := engine.Run(ctx, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Review source evidence."}}, Tools: schemas, MaxConsecutiveIdenticalToolRounds: 3})
			if converge {
				if err != nil || result.FinalMessage.Content != "Use the existing evidence." {
					t.Fatalf("recovery did not converge: %v", err)
				}
			} else {
				var stalled *agentruntime.ToolRoundNoProgressError
				if !errors.As(err, &stalled) {
					t.Fatalf("mixed narrated read loop: %v", err)
				}
			}
			if requests != 1 || model.requests > 4 {
				t.Fatalf("network=%d model=%d", requests, model.requests)
			}
		})
	}
}

func TestReadProgressTypedCachePreservesExplicitReuse(t *testing.T) {
	type sourceResult struct {
		Body string `json:"body"`
	}
	run := &sessionRunnerChatRun{}
	input := map[string]any{"query": "generic source"}
	run.storeReadReuse("source_reader", input, sourceResult{Body: "evidence"})
	result, found := run.lookupReadReuse("source_reader", input)
	if !found || mapValue(result)["reused"] != true || mapValue(result)["body"] != "evidence" {
		t.Fatalf("typed cache lost reuse: %#v", result)
	}
	if (webfetch.Result{Reused: true}).ToolResultEnvelope()["reused"] != true {
		t.Fatal("typed source status hid the reuse signal")
	}
}

func TestReadProgressEquivalentWindowsAndRunIsolation(t *testing.T) {
	run := &sessionRunnerChatRun{}
	input := map[string]any{"version_id": "source-alias", "offset": 9, "limit": 100, "human_description": "Reading"}
	page := map[string]any{"source_version_id": "immutable-v1", "content": "tail", "showing_lines": "9-10", "total_lines": 10}
	if mapValue(run.observeFileRead(input, page))["reused"] == true {
		t.Fatal("first view is new")
	}
	if input["version_id"] != "source-alias" || input["offset"] != 9 || input["limit"] != 100 {
		t.Fatal("observation mutated the executed input")
	}
	input["version_id"], input["limit"], input["human_description"] = "immutable-v1", 1000, "Reading again"
	if mapValue(run.observeFileRead(input, page))["reused"] != true {
		t.Fatal("equivalent EOF window escaped reuse")
	}
	if mapValue((&sessionRunnerChatRun{}).observeFileRead(input, page))["reused"] == true {
		t.Fatal("observation crossed task boundary")
	}
	if page["reused"] != nil {
		t.Fatal("original receipt was mutated")
	}
	input["json_pointer"] = "/other"
	if mapValue(run.observeFileRead(input, page))["reused"] == true {
		t.Fatal("distinct selection lost")
	}
}

func TestReadProgressCacheGrowthRemainsBounded(t *testing.T) {
	cache := &sessionRunnerReadReuseCache{entries: make(map[string]sessionRunnerReadReuseEntry)}
	cache.store("one", sessionRunnerReadReuseEntry{size: 1})
	cache.store("two", sessionRunnerReadReuseEntry{size: 1})
	cache.store("one", sessionRunnerReadReuseEntry{size: maxSessionRunnerReadReuseBytes})
	if cache.bytes > maxSessionRunnerReadReuseBytes || len(cache.entries) != 1 {
		t.Fatal("replacement escaped cache budget")
	}
}

func TestReadProgressEquivalentImmutableContentHandles(t *testing.T) {
	run := &sessionRunnerChatRun{}
	digest := strings.Repeat("a", 64)
	input := map[string]any{"version_id": "version-one", "offset": 1, "limit": 100}
	page := map[string]any{"source_version_id": "version-one", "source_sha256": digest, "file_path": "/cache/one", "content": "same bytes", "showing_lines": "1-1"}
	run.observeFileRead(input, page)
	input["version_id"], page["source_version_id"], page["file_path"] = "version-two", "version-two", "/cache/two"
	if mapValue(run.observeFileRead(input, page))["reused"] != true {
		t.Fatal("new storage handle looked like new evidence")
	}
	page["source_sha256"] = strings.Repeat("b", 64)
	if mapValue(run.observeFileRead(input, page))["reused"] == true {
		t.Fatal("changed source digest was ignored")
	}
}

type changingReadProgressModel struct {
	requests int
	path     string
}

func (m *changingReadProgressModel) Complete(_ context.Context, _ agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	m.requests++
	if m.requests == 7 {
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "Changing observations complete."}}, nil
	}
	if err := os.WriteFile(m.path, []byte(fmt.Sprintf("new observation %d\n", m.requests)), 0600); err != nil {
		return agentruntime.ModelResponse{}, err
	}
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: fmt.Sprintf("changing-%d", m.requests), Name: "read_file", Arguments: json.RawMessage(`{"file_path":"changing.txt","human_description":"Reading current state"}`)}}}}, nil
}

func TestReadProgressChangingContentWithoutNarrativeContinues(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	model := &changingReadProgressModel{path: filepath.Join(fixture.projectPath, "changing.txt")}
	run := &sessionRunnerChatRun{}
	schemas := []agentruntime.ToolSchema{agentWorkspaceReadFileToolSchema()}
	gateway := serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, taskRun: run, toolSchemas: schemas, hasToolSnapshot: true, suppressHooks: true}
	result, err := (agentruntime.Engine{Model: model, Tools: gateway}).Run(context.Background(), agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Read changing observations."}}, Tools: schemas, MaxConsecutiveIdenticalToolRounds: 3})
	if err != nil || model.requests != 7 || result.FinalMessage.Content != "Changing observations complete." {
		t.Fatalf("real changes were interrupted: requests=%d err=%v", model.requests, err)
	}
}

func TestReadProgressMetadataStaysWithinReadBudget(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.projectPath, "large.txt"), []byte(strings.Repeat("source text 界\n", 2000)), 0600); err != nil {
		t.Fatal(err)
	}
	gateway := serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, taskRun: &sessionRunnerChatRun{}, fileReadLimitBytes: 1024}
	input := map[string]any{"file_path": "large.txt", "human_description": "Reading bounded evidence"}
	for attempt := 0; attempt < 2; attempt++ {
		value, err := gateway.executeAgentToolResponse(context.Background(), agentruntime.ToolCall{ID: fmt.Sprintf("budget-%d", attempt), Name: "read_file"}, "read_file", input)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > 1024 {
			t.Fatalf("progress envelope exceeds reader budget: bytes=%d err=%v", len(encoded), err)
		}
	}
}

func TestReadProgressFreshMutableReadAndReplay(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	path := filepath.Join(fixture.projectPath, "changing.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{}
	gateway := serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, taskRun: run}
	input := map[string]any{"file_path": "changing.txt", "offset": 1, "limit": 1, "human_description": "Reading source"}
	read := func(input map[string]any) map[string]any {
		t.Helper()
		value, err := gateway.executeAgentToolResponse(context.Background(), agentruntime.ToolCall{ID: "read", Name: "read_file"}, "read_file", input)
		if err != nil {
			t.Fatal(err)
		}
		return mapValue(value)
	}
	first := read(input)
	if first["reused"] == true || read(input)["reused"] != true {
		t.Fatal("unchanged window was not identified")
	}
	if err := os.WriteFile(path, []byte("third\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := read(input)
	if changed["reused"] == true || !strings.Contains(stringValue(changed["content"]), "third") {
		t.Fatal("mutable source was served stale")
	}
	input["offset"] = 2
	if read(input)["reused"] == true {
		t.Fatal("new window was mistaken for repetition")
	}
	input["offset"] = 1
	resumed := &sessionRunnerChatRun{}
	fixture.server.hydrateSessionRunnerReadReuse(resumed, []eventjournal.Entry{{Message: eventjournal.Message{"type": "runner_checkpoint", "toolName": "read_file", "toolPhase": "completed", "toolInput": input, "toolResult": changed}}})
	gateway.taskRun = resumed
	if read(input)["reused"] != true {
		t.Fatal("resume lost observed window")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	value, err := gateway.executeAgentToolResponse(context.Background(), agentruntime.ToolCall{ID: "deleted", Name: "read_file"}, "read_file", input)
	if err == nil && agentruntime.ClassifyToolResult(value) == agentruntime.ToolResultSucceeded {
		t.Fatal("reuse bypassed current file authority")
	}
}
