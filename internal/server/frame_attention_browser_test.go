package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

// Real SQLite, frame/plan APIs and production React controllers. The isolated
// service does not start a model runner or reuse an installed user's data.
func TestFrameAttentionBrowser(t *testing.T) {
	if os.Getenv("SYNON_FRAME_ATTENTION_BROWSER") != "1" {
		t.Skip("enable controlled task-attention browser integration")
	}
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "attention-project", UserID: "local", Name: "Controlled attention"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"plan-review", "provider-paused"} {
		if _, err := store.CreateFrame(workspace.CreateFrameInput{
			ID: id, ProjectID: "attention-project", AgentName: "OPERON", Status: "processing", ConversationType: "agent",
		}); err != nil {
			t.Fatal(err)
		}
		stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
			UID: "frame:" + id, OwnerID: "local", ExternalID: id, SessionID: id, Kind: transcriptstore.StreamKindFrameRef,
			ProjectID: "attention-project", RootFrameID: id, FrameID: id, Epoch: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
			StreamUID: stream.UID, OwnerID: "local", ClientMessageID: id + "-input", FrameEventID: id + "-event",
			MessageUUID: id + "-message", MessageOrigin: "task_intent", Text: "Review the controlled task plan.", Destinations: []string{"ws"},
		}); err != nil {
			t.Fatal(err)
		}
		claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
			StreamUID: stream.UID, OwnerID: "local", RunnerID: id + "-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
		})
		if err != nil || !claim.Claimed {
			t.Fatalf("claim=%#v err=%v", claim, err)
		}
		plan := map[string]any{"version": 1, "task_summary": "Controlled task plan", "phases": []any{map[string]any{
			"id": "phase-1", "name": "Review", "steps": []any{map[string]any{"id": "step-1", "title": "Keep existing outputs", "description": "Review without repeating computation."}},
		}}}
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		_, version, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
			ArtifactID: id + "-plan", ProjectID: "attention-project", Name: "plan.json", ContentType: "application/json",
			Content: strings.NewReader(string(data)), MaxBytes: 1 << 20, RootFrameID: id, FrameID: id,
		})
		if err != nil {
			t.Fatal(err)
		}
		metadata := map[string]any{"_plan_artifact_id": id + "-plan", "_plan_version_id": version.ID, "_plan_json": plan}
		if id == "provider-paused" {
			metadata["_plan_control_mode"], metadata["_plan_execution_authorized"] = "autonomous", true
			if _, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
				Claim: claim.Claim, ClientMessageID: id + "-pause", ReasonCode: "provider_output_token_limit",
				ResumeDetail: "Preserve progress.", Resumable: true, AutoResume: false,
			}); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, _, _, err := repo.PauseRunnerForApproval(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
				Claim: claim.Claim, ClientMessageID: id + "-pause", Phase: transcriptstore.RunnerPhaseWaitingApproval,
				Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_plan_approval","detail":"review plan"}`),
			}); err != nil {
				t.Fatal(err)
			}
			status := "awaiting_plan_approval"
			if _, err := store.UpdateFrame(id, workspace.UpdateFrameInput{Status: &status}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.SetFrameRuntimeMetadata(id, workspace.FrameRuntimeMetadata{ContextData: metadata}); err != nil {
			t.Fatal(err)
		}
	}
	app := New(Options{Workspace: store, Transcript: repo, FileRoot: root})
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	var approvals atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Synon-User-Id", "local")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve-plan") {
			approvals.Add(1)
		}
		app.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	frontend, err := filepath.Abs("../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "tests/web-e2e/taskAttention.browser.mjs")
	command.Dir = frontend
	command.Env = append(os.Environ(), "SYNON_FRAME_ATTENTION_API="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	metadata, _, err := store.GetFrameRuntimeMetadata("plan-review")
	if err != nil || metadata.ContextData["_plan_approved"] != true || approvals.Load() != 1 {
		t.Fatalf("approval was not durably applied once: count=%d error=%v", approvals.Load(), err)
	}
	dispatch, found, err := store.GetCompatibilityFrameResumeDispatchByFrame("provider-paused")
	if err != nil || !found || dispatch.Status != "registered" {
		t.Fatalf("pause did not retain a durable resume dispatch: found=%t error=%v", found, err)
	}
	fmt.Println(string(output))
}
