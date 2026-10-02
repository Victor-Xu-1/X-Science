package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	secretstore "synon-go/internal/persistence/secrets"
	workspace "synon-go/internal/persistence/workspace"
)

// All recovery paths must preserve work and bound an unchanged generation route.
// All databases, settings and provider responses belong to this local fixture.
func TestDurableGenerationFixedProviderCapacityMustBoundUnproductiveContinuation(t *testing.T) {
	for _, incompleteTool := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("incomplete_tool_%t_explicit_limit_%t", incompleteTool, explicit), func(t *testing.T) {
				store, repo, _ := newTranscriptWebFixture(t)
				seedTranscriptWebFrame(t, store, "local", "recovery-project", "recovery-frame")
				var requests atomic.Int64
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var input struct {
						MaxTokens int `json:"max_tokens"`
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
						return
					}
					sequence := requests.Add(1)
					if explicit && sequence == 1 && input.MaxTokens != 2048 {
						t.Errorf("saved initial allowance changed: %d", input.MaxTokens)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if incompleteTool {
						fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-%d\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"{\"}}]},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2048}}\n\ndata: [DONE]\n\n", sequence)
						return
					}
					fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fragment%d\"},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2048}}\n\ndata: [DONE]\n\n", sequence)
				}))
				defer api.Close()
				srv := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
				t.Cleanup(func() { _ = srv.Close(context.Background()) })
				if _, err := srv.settingsStore.Set("model.activeProviderId", "recovery-provider"); err != nil {
					t.Fatal(err)
				}
				if _, err := srv.secretStore.Create(secretstore.Secret{ID: "recovery-key", UserID: "local", Provider: "openai", Value: "local-test-only"}); err != nil {
					t.Fatal(err)
				}
				var limit *int
				if explicit {
					value := 2048
					limit = &value
				}
				enabled := true
				if _, err := store.RegisterModelProvider(workspace.ModelProviderInput{
					ID: "recovery-provider", UserID: "local", Name: "Recovery", Type: "openai",
					BaseURL: api.URL + "/v1", Model: "recovery-model", SecretRef: "secret://recovery-key", Enabled: &enabled, MaxTokens: limit,
				}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
					FrameID: "recovery-frame", MessageUUID: "recovery-message", ClientMessageID: "recovery-input",
					Text: "Write a complete answer and preserve earlier work.",
				}); err != nil {
					t.Fatal(err)
				}
				options := SessionRunnerChatOptions{SessionID: "recovery-frame", LeaseTTL: time.Minute, RequestTimeout: time.Second,
					MaxAttempts: 1, RequireSavedModel: true, DisableSkillDiscovery: true, DisableMCPDiscovery: true}
				for cycle := 1; cycle <= 8; cycle++ {
					options.RunnerID = fmt.Sprintf("recovery-runner-%d", cycle)
					result, err := srv.RunSessionRunnerChatOnce(context.Background(), options)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("cycle=%d requests=%d status=%s reason=%s auto_resume=%t awaiting_condition=%t", cycle, requests.Load(), result.Status, result.InterruptionReasonCode, result.InterruptionAutoResume, result.AwaitingRecoveryCondition)
					if result.AwaitingRecoveryCondition && !result.InterruptionAutoResume {
						stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", "recovery-frame")
						if err != nil || !found {
							t.Fatalf("recovery stream: %t %v", found, err)
						}
						if _, eligible, err := repo.GetAutoResumeCandidate(context.Background(), stream.UID, stream.OwnerID); err != nil || eligible {
							t.Fatalf("parked route is still dispatchable: %t %v", eligible, err)
						}
						before := requests.Load()
						options.SessionID = ""
						if _, err := srv.RunSessionRunnerChatOnce(context.Background(), options); err != nil {
							t.Fatal(err)
						}
						if requests.Load() != before {
							t.Fatal("unattended cycle invoked provider after parking")
						}
						return
					}
					options.SessionID = ""
				}
				t.Fatal("unchanged, unproductive provider route still auto-resumes after 8 local cycles")
			})
		}
	}
}

func TestDurableGenerationRepeatedExecutionObservationIsNotProgress(t *testing.T) {
	run := &sessionRunnerChatRun{}
	receipt := map[string]any{"ok": true, "exit_code": 0, "files_written": []any{}, "stdout": "same files", "exec_id": "first", "cell_index": 1, "kernel_id": "initial-kernel"}
	if !runnerGenerationToolProgress("bash", run.observeExecutionEvidence("bash", receipt)) {
		t.Fatal("first observation should be evidence")
	}
	receipt["exec_id"], receipt["cell_index"] = "second", 2
	if runnerGenerationToolProgress("bash", run.observeExecutionEvidence("bash", receipt)) {
		t.Fatal("same observation counted as fresh evidence")
	}
	receipt["kernel_id"] = "replacement-kernel"
	if runnerGenerationToolProgress("bash", run.observeExecutionEvidence("bash", receipt)) {
		t.Fatal("kernel replacement counted identical output as fresh evidence")
	}
	receipt["stdout"] = "new useful result"
	if !runnerGenerationToolProgress("bash", run.observeExecutionEvidence("bash", receipt)) {
		t.Fatal("new result did not advance")
	}
	receipt["files_written"] = []any{map[string]any{"path": "output.txt", "sha256": strings.Repeat("a", 64)}}
	if !runnerGenerationToolProgress("bash", run.observeExecutionEvidence("bash", receipt)) {
		t.Fatal("mutation was suppressed")
	}
}

func TestDurableGenerationOrphanPunctuationIsNotPublished(t *testing.T) {
	if text := sessionRunnerPublicProgressNarration("_"); text != "" {
		t.Fatalf("orphan transport fragment accepted as public progress: %q", text)
	}
}

func TestDurableGenerationReadObservationSurvivesPreparationCheckpointChurn(t *testing.T) {
	fixture := newNoProgressReceiptFixture(t)
	input := map[string]any{"file_path": "evidence.txt"}
	page := map[string]any{"content": "unchanged source bytes", "file_path": "evidence.txt", "showing_lines": "1-1", "total_lines": 1}
	first := fixture.run.observeFileRead(input, page)
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	fixture.complete(t, agentruntime.ToolCall{ID: "recovery-file-read", Name: "read_file", Arguments: json.RawMessage(`{"file_path":"evidence.txt"}`)}, string(encoded))
	for i := 0; i < 220; i++ {
		appendRunnerToolCheckpoint(t, fixture.repo, fixture.run.Transcript.Claim, fmt.Sprintf("recovery-preparation-%d", i), map[string]any{"status": "running", "stage": "model_execution"})
	}
	for _, limit := range []int{1000, 200} {
		t.Run(fmt.Sprintf("checkpoint_window_%d", limit), func(t *testing.T) {
			resumed := &sessionRunnerChatRun{SessionID: fixture.run.SessionID, Transcript: fixture.run.Transcript}
			entries, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), fixture.run.Transcript, limit, limit, resumed)
			if err != nil {
				t.Fatal(err)
			}
			observed := mapValue(resumed.observeFileRead(input, page))
			t.Logf("replayed_entries=%d result_reused=%v effect=%v", len(entries), observed["reused"], observed["effect"])
			if observed["reused"] != true {
				t.Fatal("bounded provider history lost durable knowledge of the unchanged read")
			}
		})
	}
}

type recoveryStructuredProgressModel struct{}

func (recoveryStructuredProgressModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "_", ToolCalls: []agentruntime.ToolCall{
		{ID: "recovery-call", Name: "read_file", Arguments: json.RawMessage(`{"file_path":"evidence.txt","public_progress":"The source file is available. I will inspect the relevant evidence before continuing."}`)},
	}}}, nil
}

func TestDurableGenerationFragmentMustNotOverrideStructuredProgress(t *testing.T) {
	client := sessionRunnerResponseContractClient{delegate: recoveryStructuredProgressModel{}, progressDue: func() bool { return true }}
	response, err := client.Complete(context.Background(), agentruntime.ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content == "_" {
		t.Fatal("orphan native fragment won over complete structured progress")
	}
}
