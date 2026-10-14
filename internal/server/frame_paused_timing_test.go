package server

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestFramePausedTimingSurvivesReloadAndStoreRestart(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workspace.db")
	store, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			_ = store.Close()
		}
	}()
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "timing-project", UserID: "local", Name: "Timing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "timing-frame", ProjectID: "timing-project", AgentName: "OPERON",
		Status: "running", ConversationType: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:timing-frame", OwnerID: "local", ExternalID: "timing-frame", SessionID: "timing-frame",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "timing-project",
		RootFrameID: "timing-frame", FrameID: "timing-frame", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "timing-input",
		FrameEventID: "timing-input-frame", MessageUUID: "timing-message", MessageOrigin: "task_intent",
		Text: "Check duration", Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "timing-runner",
		TTL: time.Hour, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	interruption, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: claim.Claim, ClientMessageID: "timing-pause", ReasonCode: "model_provider_unavailable",
		Resumable: true, AutoResume: false, Destinations: []string{"ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Age this disposable fixture without a wall-clock sleep. The interruption
	// already expires the lease; elapsed time must not subtract this idle day.
	pausedAt := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`UPDATE transcript_runner_attempts SET claimed_at=?,expires_at=? WHERE stream_uid=?`, pausedAt.Add(-90*time.Second), pausedAt, stream.UID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE transcript_events SET created_at=? WHERE stream_uid=? AND event_id=?`, pausedAt, stream.UID, interruption.Event.EventID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	assertTiming := func() {
		t.Helper()
		repository, err := store.TranscriptRepository(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		app := New(Options{Workspace: store, Transcript: repository, FileRoot: root}).Handler()
		for range 2 {
			got := compatJSONRequest(t, app, http.MethodGet, "/api/frames/timing-frame", "local", nil, http.StatusOK)
			if got["runtime_paused"] != true || got["runtime_active"] != false || got["runtime_task_active"] != false {
				t.Fatalf("pause=%#v", got)
			}
			for _, key := range []string{"runtime_elapsed_ms", "runtime_task_elapsed_ms"} {
				if got[key] != float64(90_000) {
					t.Fatalf("%s=%v, want persisted 90000", key, got[key])
				}
			}
		}
	}
	assertTiming()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	assertTiming()
	repository, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	legacyDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacyDB.Close() })
	// A pre-upgrade same-attempt resume has no immutable generation endpoint.
	// Preserve that absence instead of manufacturing a precise historical clock.
	if _, err := legacyDB.Exec(`UPDATE transcript_runner_attempts SET resume_source='checkpoint',resume_checkpoint_sequence=? WHERE stream_uid=?`, interruption.Checkpoint.Sequence, stream.UID); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Workspace: store, Transcript: repository, FileRoot: root}).Handler()
	assertUnavailable := func() {
		t.Helper()
		got := compatJSONRequest(t, app, http.MethodGet, "/api/frames/timing-frame", "local", nil, http.StatusOK)
		if got["runtime_elapsed_available"] != false || got["runtime_task_elapsed_available"] != false || got["runtime_elapsed_ms"] != nil || got["runtime_task_elapsed_ms"] != nil {
			t.Fatalf("unknown historical timing became a fake duration: %#v", got)
		}
	}
	assertUnavailable()
	if err := repository.RunImmediate(context.Background(), func(tx *transcriptstore.ImmediateTransaction) error {
		_, _, _, err := tx.FinishLatestFrameRunner(context.Background(), stream.OwnerID, stream.FrameID, "cancelled", "controlled late settlement", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertUnavailable()
	if _, _, _, err := repository.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "timing-next-input", FrameEventID: "timing-next-frame", MessageUUID: "timing-next-message", Text: "controlled pending input"}); err != nil {
		t.Fatal(err)
	}
	got := compatJSONRequest(t, app, http.MethodGet, "/api/frames/timing-frame", "local", nil, http.StatusOK)
	if got["runtime_stage"] != "starting" || got["runtime_elapsed_available"] != true || got["runtime_task_elapsed_available"] != false || got["runtime_task_elapsed_ms"] != nil {
		t.Fatalf("pending input erased whole-task timing uncertainty: %#v", got)
	}
}
