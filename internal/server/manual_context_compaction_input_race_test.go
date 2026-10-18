package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionNewPromptAndQueuedInputDrainOnlyMaintenance(t *testing.T) {
	for _, delivery := range []string{"normal", "queued-before-claim", "queued-after-claim", "queue-wins-admission"} {
		t.Run(delivery, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			if delivery == "normal" {
				// This path crosses the real public assistant-authority check;
				// the persistence-only base fixture intentionally has no catalog.
				enabled := true
				if _, err := f.store.CreateAgent(workspace.CreateAgentInput{
					ID: "maintenance-followup-agent", UserID: f.stream.OwnerID,
					Name: f.identity.access.Frame.AgentName, DisplayName: "Follow-up assistant",
					Description:  "Accepts an ordinary prompt after context maintenance.",
					Unrestricted: true, Enabled: &enabled,
				}); err != nil {
					t.Fatal(err)
				}
			}
			finishSkillControlInput(t, f)
			request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
			if delivery == "queue-wins-admission" {
				_, _, _, err := f.store.QueueCompatibilityMessage(f.stream.FrameID, "pending-before-maintenance", map[string]any{"request": "New task"},
					map[string]any{"text": "New task", "inputData": map[string]any{"request": "New task"}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); !errors.Is(err, transcriptstore.ErrContextCompactionBusy) {
					t.Fatalf("queued input did not win maintenance admission: %v", err)
				}
				return
			}
			if _, err := f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); err != nil {
				t.Fatal(err)
			}
			dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("manual-race-worker", 30e9)
			if err != nil || !claimed {
				t.Fatalf("dispatch=%#v claimed=%t err=%v", dispatch, claimed, err)
			}
			if delivery == "queued-after-claim" {
				claimSkillControlInput(t, f)
			}
			if delivery == "normal" {
				response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages",
					map[string]any{"content": "A different ordinary question.", "files": []any{}, "loading_id": "normal-after-maintenance"}, f.stream.OwnerID)
				if response.Code != http.StatusOK && response.Code != http.StatusAccepted {
					t.Fatalf("normal send HTTP %d: %s", response.Code, response.Body.String())
				}
			} else {
				// A Continue here follows maintenance, not a completed scientific turn.
				// It must not be discarded by the old completed-task queue rule.
				_, _, _, err = f.store.QueueCompatibilityMessage(f.stream.FrameID, "queued-after-maintenance", map[string]any{"request": "Continue"},
					map[string]any{"text": "Continue", "inputData": map[string]any{"request": "Continue"}})
				if err != nil {
					t.Fatal(err)
				}
				count, err := f.server.recoverCompatibilityQueuedMessages()
				if err != nil || count != 1 {
					t.Fatalf("queue drained=%d err=%v", count, err)
				}
				queued, found, err := f.store.GetCompatibilityMessageIntent("queued-after-maintenance")
				if err != nil || !found || queued.State != "drained" {
					t.Fatalf("queued=%#v found=%t err=%v", queued, found, err)
				}
			}
			receipt, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
			if err != nil || receipt.State != "cancelled" {
				t.Fatalf("maintenance=%#v err=%v", receipt, err)
			}
			var chat SessionRunnerChatOptions
			if err = f.server.configureTranscriptFrameResume(context.Background(), dispatch, &chat); !errors.Is(err, transcriptstore.ErrClaimStale) {
				t.Fatalf("old dispatch borrowed new prompt: %v", err)
			}
			stream, err := f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			if stream.InputRevision != request.Expected.InputRevision+2 || stream.ConsumedInputRevision != request.Expected.InputRevision+1 {
				t.Fatalf("new prompt admission/consumption damaged: %#v", stream)
			}
		})
	}
}
