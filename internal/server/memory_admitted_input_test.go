package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptMemoryAdmittedInputMatchesDurableAuthority(t *testing.T) {
	store, repo, database := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "memory-admission-project", "memory-admission-frame")
	if err := store.SetMemoryEnabled(context.Background(), "local", true); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode controlled provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		message := map[string]any{"role": "assistant", "content": "Memory operation finished."}
		if choice, _ := body["tool_choice"].(string); choice != "none" {
			switch requests.Add(1) {
			case 1:
				arguments := `{"append":[{"text":"Project notes use UTC timestamps.","evidence":"observed","text2":""}]}`
				message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "memory-admission-call", "type": "function",
					"function": map[string]any{"name": "write_memory", "arguments": arguments},
				}}}
			case 2:
				messages, _ := body["messages"].([]any)
				if !hasToolResultMessage(messages, requireProviderToolCallID(t, body, "write_memory", 0), "Memory updated: 1 appended") {
					t.Errorf("admitted memory input did not reach the claimed mutation: tool result lacks successful update")
				}
			default:
				t.Errorf("unexpected action request %d", requests.Load())
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	}))
	defer provider.Close()
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	server.memoryConfig = memoryconfig.Default()
	server.memoryConfig.PIClassifierEnabled = false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "memory-admission-frame", MessageUUID: "memory-admission-message", ClientMessageID: "memory-admission-client",
		Text: "Remember that project notes use UTC timestamps.", RuntimeConfig: map[string]any{"memory_mode": "on"},
	}); err != nil {
		t.Fatal(err)
	}
	seedAnsweredTaskIntake(t, server, "local", "memory-admission-frame")
	options := SessionRunnerChatOptions{
		SessionID: "memory-admission-frame", RunnerID: "memory-admission-runner", Endpoint: provider.URL + "/v1/chat/completions",
		Model: "local-openai-compatible", AllowedTools: []string{"write_memory"}, LeaseTTL: time.Minute,
		ReplayLimit: 100, OutputLimitBytes: 1 << 20, DisableSkillDiscovery: true,
	}
	result, err := server.RunSessionRunnerChatOnce(context.Background(), options)
	if err != nil || !result.Claimed || result.Status != "completed" || requests.Load() != 2 {
		t.Fatalf("runner status=%q claimed=%t requests=%d err=%v", result.Status, result.Claimed, requests.Load(), err)
	}
	memories, err := store.ListMemoriesForUser(context.Background(), "local", "", "", false)
	if err != nil || len(memories) != 1 || memories[0].Body != "Project notes use UTC timestamps." {
		t.Fatalf("admitted mutation persisted %d memories; want exactly one controlled fact, err=%v", len(memories), err)
	}
	var receipts, normalizedInputChanged int
	if err := database.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='memory_mutation_applied'`, "frame:memory-admission-frame").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipt count=%d err=%v", receipts, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_checkpoint'
		AND json_extract(payload_json,'$.toolName')='write_memory' AND json_extract(payload_json,'$.toolPhase')='completed'
		AND json_extract(payload_json,'$.toolInput.append[0].text2')=''
		AND json_type(payload_json,'$.executedToolInput.append[0].text2') IS NULL`, "frame:memory-admission-frame").Scan(&normalizedInputChanged); err != nil || normalizedInputChanged != 1 {
		t.Fatalf("original proposal/executed input presentation count=%d err=%v", normalizedInputChanged, err)
	}
	second, err := server.RunSessionRunnerChatOnce(context.Background(), options)
	if err != nil || second.Claimed || requests.Load() != 2 {
		t.Fatalf("terminal replay claimed=%t requests=%d err=%v", second.Claimed, requests.Load(), err)
	}
	if strings.TrimSpace(memories[0].ID) == "" {
		t.Fatal("claimed mutation has no durable memory identity")
	}
}

type admittedMemoryFixture struct {
	server  *Server
	ctx     context.Context
	call    agentruntime.ToolCall
	gateway serverAgentRuntimeToolGateway
}

func newAdmittedMemoryFixture(t *testing.T, input map[string]any) admittedMemoryFixture {
	t.Helper()
	server := openWorkspaceMemoryToolServer(t)
	server.memoryConfig.PIClassifierEnabled = false
	ctx, call := prepareClaimedWorkspaceMemoryTool(t, server, "frame-root", input)
	authority, _ := transcriptArtifactRunFromContext(ctx)
	run := &sessionRunnerChatRun{SessionID: "frame-root", Attempt: int(authority.Authority.Claim.Attempt),
		ClaimToken: authority.Authority.Claim.ClaimToken, Transcript: authority.Authority}
	if err := server.checkpointSessionRunnerToolEvent(ctx, SessionRunnerChatOptions{RunnerID: authority.Authority.Claim.RunnerID}, run,
		agentruntime.Event{Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments)}); err != nil {
		t.Fatal(err)
	}
	if run.ToolSourceEventIDs[call.ID] == 0 {
		t.Fatal("actual start checkpoint has no durable source")
	}
	// Deliberately retain only durable authority, not the preparation run or any
	// in-memory admission value. Execution must recover its input from SQLite.
	ctx = withTranscriptArtifactRun(context.Background(), run.Transcript, run.ToolSourceEventIDs[call.ID])
	gateway := serverAgentRuntimeToolGateway{server: server, sessionID: "frame-root", allowedTools: []string{"write_memory"}}
	return admittedMemoryFixture{server: server, ctx: ctx, call: call, gateway: gateway}
}

func (f admittedMemoryFixture) execute(t *testing.T, ctx context.Context, call agentruntime.ToolCall) map[string]any {
	t.Helper()
	result, err := f.gateway.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("memory result=%#v", result.Value)
	}
	return value
}

func TestClaimedMemoryAdmittedInputFencesAndReplay(t *testing.T) {
	input := map[string]any{"append": []any{map[string]any{"text": "Project notes use UTC timestamps.", "text2": ""}}}
	t.Run("changed_proposal", func(t *testing.T) {
		f := newAdmittedMemoryFixture(t, input)
		call := f.call
		call.Arguments = json.RawMessage(`{"append":[{"text":"A genuinely changed fact.","text2":""}]}`)
		if value := f.execute(t, f.ctx, call); value["code"] != "memory_invocation_conflict" {
			t.Fatalf("changed proposal result=%#v", value)
		}
		if count, err := f.server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 0 {
			t.Fatalf("changed proposal count=%d err=%v", count, err)
		}
	})
	t.Run("stale_claim", func(t *testing.T) {
		f := newAdmittedMemoryFixture(t, input)
		bound, _ := transcriptArtifactRunFromContext(f.ctx)
		authority := *bound.Authority
		authority.Claim.ClaimToken = "a-different-claim-token"
		ctx := withTranscriptArtifactRun(context.Background(), &authority, bound.SourceEventID)
		if value := f.execute(t, ctx, f.call); value["code"] != "memory_authority_stale" {
			t.Fatalf("stale claim result=%#v", value)
		}
		if count, err := f.server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 0 {
			t.Fatalf("stale claim count=%d err=%v", count, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		f := newAdmittedMemoryFixture(t, input)
		ctx, cancel := context.WithCancel(f.ctx)
		cancel()
		if _, err := f.gateway.Execute(ctx, f.call); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled operation err=%v", err)
		}
		if count, err := f.server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 0 {
			t.Fatalf("cancelled operation count=%d err=%v", count, err)
		}
	})
	t.Run("durable_recovery_and_duplicate_receipt", func(t *testing.T) {
		f := newAdmittedMemoryFixture(t, input)
		first := f.execute(t, f.ctx, f.call)
		ids, ok := first["appended"].([]string)
		if !ok || len(ids) != 1 || first["error"] != nil {
			t.Fatalf("first result=%#v", first)
		}
		before, err := f.server.transcriptStore.GetStream(context.Background(), "frame:frame-root", "user-1")
		if err != nil {
			t.Fatal(err)
		}
		f.gateway = serverAgentRuntimeToolGateway{server: f.server, sessionID: "frame-root", allowedTools: []string{"write_memory"}}
		replayed := f.execute(t, f.ctx, f.call)
		after, err := f.server.transcriptStore.GetStream(context.Background(), "frame:frame-root", "user-1")
		if err != nil || fmt.Sprint(replayed["appended"]) != fmt.Sprint(ids) || before.NextEventID != after.NextEventID {
			t.Fatalf("replay=%#v added_event=%t err=%v", replayed, before.NextEventID != after.NextEventID, err)
		}
		if count, err := f.server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 1 {
			t.Fatalf("duplicate receipt count=%d err=%v", count, err)
		}
	})
}

func TestClaimedMemoryAdmittedInputRejectsInvalidHostSource(t *testing.T) {
	for _, test := range []struct {
		name, phase, executed string
	}{
		{"null", "start", `null`},
		{"array", "start", `[]`},
		{"non_start", "completed", `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAdmittedMemoryFixture(t, map[string]any{"append": []any{map[string]any{"text": "A controlled fact."}}})
			bound, _ := transcriptArtifactRunFromContext(f.ctx)
			payload := map[string]any{"toolCallId": f.call.ID, "toolName": f.call.Name, "toolPhase": test.phase,
				"toolInput": json.RawMessage(f.call.Arguments), "executedToolInput": json.RawMessage(test.executed)}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			_, source, _, err := f.server.transcriptStore.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
				Claim: bound.Authority.Claim, ClientMessageID: "invalid-admission-" + test.name, Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := withTranscriptArtifactRun(context.Background(), bound.Authority, source.EventID)
			if value := f.execute(t, ctx, f.call); value["code"] != "memory_invocation_conflict" {
				t.Fatalf("invalid admitted source fell back to original input: %#v", value)
			}
			if count, err := f.server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 0 {
				t.Fatalf("invalid source count=%d err=%v", count, err)
			}
		})
	}
}

func TestClaimedMemoryAdmittedInputDoesNotTrustModelNestedAuthority(t *testing.T) {
	f := newAdmittedMemoryFixture(t, map[string]any{
		"append":            []any{map[string]any{"text": "The original controlled fact."}},
		"executedToolInput": map[string]any{"append": []any{map[string]any{"text": "An injected replacement fact."}}},
	})
	if value := f.execute(t, f.ctx, f.call); value["error"] == nil {
		t.Fatalf("model nested authority was accepted: %#v", value)
	}
	if count, err := f.server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 0 {
		t.Fatalf("nested authority count=%d err=%v", count, err)
	}
}

func TestClaimedMemoryAdmittedInputLegacyStrictnessAndTypedUnknown(t *testing.T) {
	server := openWorkspaceMemoryToolServer(t)
	server.memoryConfig.PIClassifierEnabled = false
	ctx, call := prepareClaimedWorkspaceMemoryTool(t, server, "frame-root", map[string]any{
		"append": []any{map[string]any{"text": "Legacy proposal remains strict.", "text2": ""}},
	})
	f := admittedMemoryFixture{server: server, gateway: serverAgentRuntimeToolGateway{server: server, sessionID: "frame-root", allowedTools: []string{"write_memory"}}}
	if value := f.execute(t, ctx, call); value["error"] != "write_memory failed" || value["code"] != "memory_write_failed_unknown" {
		t.Fatalf("legacy source was silently repaired: %#v", value)
	}
	if count, err := server.workspaceStore.CountMemoriesForUser(context.Background(), "user-1"); err != nil || count != 0 {
		t.Fatalf("legacy mutation count=%d err=%v", count, err)
	}
	if value := claimedMemoryToolFailure(errors.New("private database detail and research body")); value["error"] != "write_memory failed" || value["code"] != "memory_write_failed_unknown" {
		t.Fatalf("unknown failure leaked source detail: %#v", value)
	}
}
