package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestWebInputDurabilityFailureCannotQueueAndRetryKeepsOneFrozenInput(t *testing.T) {
	ctx := context.Background()
	app, store := newP3WebConversationServer(t)
	repo, err := store.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	app.transcriptStore = repo
	project := createP3Project(t, store, "durable-queue-project", "local")
	frame, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "durable-queue-frame", ProjectID: project.ID, AgentName: "OPERON", Status: "processing", ConversationType: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "frame:" + frame.ID, OwnerID: "local", ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: project.ID, RootFrameID: frame.ID, FrameID: frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial", FrameEventID: "initial-event",
		MessageUUID: "initial-message", Text: "Keep the current task active.", Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("initial input created=%t err=%v", created, err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "actual-active-owner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("initial owner claimed=%t err=%v", claimed.Claimed, err)
	}
	source := filepath.Join(t.TempDir(), "queued-evidence.txt")
	if err := os.WriteFile(source, []byte("queued bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.workspaceDirectorySyncer = func(directory *os.File) error {
		names, err := directory.Readdirnames(-1)
		if err != nil {
			return err
		}
		if slices.Contains(names, "queued-evidence.txt") {
			return errors.New("controlled queued-file directory rejection")
		}
		return directory.Sync()
	}
	const loadingID = "durable-queued-message"
	messageID := transcriptstore.FrameMessageIntentID(frame.ID, loadingID, "")
	body := map[string]any{"content": "Use the queued evidence.", "files": []string{source}, "loading_id": loadingID}
	url := "/api/conversations/" + frame.ID + "/messages"
	failed := p3JSONRequest(t, app, http.MethodPost, url, body, "local")
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("uncertain input became queued: %d %s", failed.Code, failed.Body.String())
	}
	if intent, found, err := store.GetCompatibilityMessageIntent(messageID); err != nil || found {
		t.Fatalf("failed publication left an accepted queue intent: %#v found=%t err=%v", intent, found, err)
	}
	app.workspaceDirectorySyncer = nil
	for attempt := 0; attempt < 2; attempt++ {
		response := p3JSONRequest(t, app, http.MethodPost, url, body, "local")
		if response.Code != http.StatusAccepted {
			t.Fatalf("durable queue/replay failed: attempt=%d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
		intent, found, err := store.GetCompatibilityMessageIntent(messageID)
		if err != nil || !found || intent.State != "queued" || intent.FrameID != frame.ID {
			t.Fatalf("missing exact queued intent: %#v found=%t err=%v", intent, found, err)
		}
		input, ok := intent.DeliveryPayload["inputData"].(map[string]any)
		files, fileOK := input["files"].([]any)
		if !ok || !fileOK || len(files) != 1 || files[0] != "inputs/queued-evidence.txt" {
			t.Fatalf("queued input did not retain the owned file: %#v", intent.DeliveryPayload)
		}
		current, found, err := repo.GetFrameStreamBySession(ctx, "local", frame.ID)
		if err != nil || !found || current.InputRevision != 1 {
			t.Fatalf("queue bypassed the active input: %#v found=%t err=%v", current, found, err)
		}
	}
}
