package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestFrameAttentionProjectionRequiresAnActualApprovalBoundary(t *testing.T) {
	for _, test := range []struct {
		name, frameStatus, reason, want string
		context                         map[string]any
	}{
		{"automatic plan output interruption", "processing", "provider_output_token_limit", "paused", map[string]any{
			"_plan_control_mode": "autonomous", "_plan_execution_authorized": true,
		}},
		{"approved plan provider interruption", "processing", "model_provider_unavailable", "paused", map[string]any{
			"_plan_approved": true,
		}},
		{"unapproved draft is not a request", "processing", "provider_output_token_limit", "paused", nil},
		{"explicit plan review", "awaiting_plan_approval", "plan_approval_required", "awaiting_plan_approval", nil},
		{"real input takes precedence", "processing", "provider_output_token_limit", "awaiting_user_response", map[string]any{
			"_plan_control_mode": "autonomous", "_plan_execution_authorized": true,
			"_pending_input_requests": []any{map[string]any{"request_id": "question-1", "kind": "ask_user"}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := workspace.Open(filepath.Join(root, "workspace.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			const projectID, frameID = "attention-project", "attention-frame"
			if _, err := store.CreateProject(workspace.CreateProjectInput{ID: projectID, UserID: "local", Name: "Attention"}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateFrame(workspace.CreateFrameInput{
				ID: frameID, ProjectID: projectID, AgentName: "OPERON", Status: test.frameStatus, ConversationType: "agent",
			}); err != nil {
				t.Fatal(err)
			}
			metadata := map[string]any{"_plan_artifact_id": "current-plan", "_plan_version_id": "current-version"}
			for key, value := range test.context {
				metadata[key] = value
			}
			if _, err := store.SetFrameRuntimeMetadata(frameID, workspace.FrameRuntimeMetadata{ContextData: metadata}); err != nil {
				t.Fatal(err)
			}
			repository, err := store.TranscriptRepository(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			stream, err := repository.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
				UID: "frame:" + frameID, OwnerID: "local", ExternalID: frameID, SessionID: frameID,
				Kind: transcriptstore.StreamKindFrameRef, ProjectID: projectID, RootFrameID: frameID, FrameID: frameID, Epoch: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := repository.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "attention-input", FrameEventID: "attention-event",
				MessageUUID: "attention-message", MessageOrigin: "task_intent", Text: "Inspect task attention.", Destinations: []string{"ws"},
			}); err != nil {
				t.Fatal(err)
			}
			claim, err := repository.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
				StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "attention-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%#v err=%v", claim, err)
			}
			if _, err := repository.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
				Claim: claim.Claim, ClientMessageID: "attention-interruption", ReasonCode: test.reason,
				ResumeDetail: "Preserve existing results and wait for recovery.", Resumable: true, AutoResume: false, Destinations: []string{"ws"},
			}); err != nil {
				t.Fatal(err)
			}
			app := New(Options{Workspace: store, Transcript: repository, FileRoot: root})
			t.Cleanup(func() { _ = app.Close(context.Background()) })
			projected := compatJSONRequest(t, app.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
			if projected["status"] != test.want || projected["runtime_active"] != false {
				t.Fatalf("wanted %s without active execution, got %#v", test.want, projected)
			}
			output := projected["output_data"].(map[string]any)
			if output["plan_artifact_id"] != "current-plan" || output["plan_version_id"] != "current-version" {
				t.Fatalf("plan navigation was lost: %#v", output)
			}
			if projected["runtime_interruption_reason"] != test.reason {
				t.Fatalf("recovery reason was lost: %#v", projected)
			}
		})
	}
}
