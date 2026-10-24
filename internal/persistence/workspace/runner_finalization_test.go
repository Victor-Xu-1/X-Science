package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func atomicFinalizationFixture(t *testing.T) (*Store, *transcriptstore.Repository, RunnerFinalizationInput) {
	t.Helper()
	store, repo, stream, claim := newParkAskUserTranscriptFixture(t)
	if _, err := store.db.Exec(`UPDATE frames SET name='Aside atomic fixture' WHERE id=?`, stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetFrameRuntimeMetadata(stream.FrameID, FrameRuntimeMetadata{FrameID: stream.FrameID, ContextData: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFrameOutputData(stream.FrameID, map[string]any{"response": "previous response"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMemoryEnabled(t.Context(), stream.OwnerID, true); err != nil {
		t.Fatal(err)
	}
	assistant, _ := json.Marshal(map[string]any{"text": "Exact synthetic final response.", "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	terminal, _ := json.Marshal(map[string]any{"status": "completed", "detail": "runner chat completed", "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	input := RunnerFinalizationInput{Transcript: transcriptstore.RunnerFinalizationInput{
		Assistant: &transcriptstore.AppendEventInput{Claim: claim, ClientMessageID: "atomic-assistant", Type: "assistant_message", Source: transcriptstore.EventSourcePayload, PayloadJSON: assistant, Destinations: []string{"ws"}},
		Finish:    transcriptstore.FinishRunnerInput{Claim: claim, ClientMessageID: "atomic-finish", Status: "completed", PayloadJSON: terminal, Destinations: []string{"ws"}},
	}, Memory: &MemoryExtractionCompletion{Mode: "compact", ExtractEveryN: 1, Model: MemoryExtractionModelIdentity{Model: "synthetic-original-model"}}}
	return store, repo, input
}

func atomicFinalizationCounts(t *testing.T, store *Store, input RunnerFinalizationInput, want int, response string) {
	t.Helper()
	queries := []string{
		`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='assistant_message'`,
		`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_finished'`,
		`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`,
	}
	for _, query := range queries {
		var count int
		if err := store.db.QueryRow(query, input.Transcript.Finish.Claim.StreamUID).Scan(&count); err != nil || count != want {
			t.Fatalf("partial/duplicate finalization: count=%d want=%d err=%v", count, want, err)
		}
	}
	var dispatches, ordinal int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM workspace_outbox WHERE topic=?`, MemoryExtractionOutboxTopic).Scan(&dispatches); err != nil || dispatches != want {
		t.Fatalf("Memory dispatch count=%d want=%d err=%v", dispatches, want, err)
	}
	if err := store.db.QueryRow(`SELECT COALESCE((SELECT memory_completion_ordinal FROM project_runtime_metadata WHERE project_id='project'),0)`).Scan(&ordinal); err != nil || ordinal != want {
		t.Fatalf("Memory cadence=%d want=%d err=%v", ordinal, want, err)
	}
	output, found, err := store.GetFrameOutputData("frame")
	if err != nil || !found || output["response"] != response {
		t.Fatalf("Aside output=%#v found=%t err=%v", output, found, err)
	}
}

func TestAtomicFinalizationRollsBackMemoryAndAsideWithLateSQLFailure(t *testing.T) {
	store, _, input := atomicFinalizationFixture(t)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_atomic_aside BEFORE UPDATE OF output_data ON frame_runtime_metadata WHEN NEW.frame_id='frame'
		BEGIN SELECT RAISE(ABORT,'synthetic late Aside persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result, outcome, err := store.FinalizeRunnerTranscript(ctx, input)
	if err == nil || outcome.KnownCommit() || outcome.Transaction.CommitAttempted || result.Transcript.Terminal.EventID != 0 {
		t.Fatalf("late callback failure was accepted: result=%#v outcome=%#v err=%v", result, outcome, err)
	}
	atomicFinalizationCounts(t, store, input, 0, "previous response")
	if _, err := store.db.Exec(`DROP TRIGGER reject_atomic_aside`); err != nil {
		t.Fatal(err)
	}
	result, outcome, err = store.FinalizeRunnerTranscript(ctx, input)
	if err != nil || !outcome.KnownCommit() || result.EnqueuedMemory == nil || result.Aside == nil {
		t.Fatalf("finalization after known rollback: result=%#v outcome=%#v err=%v", result, outcome, err)
	}
	atomicFinalizationCounts(t, store, input, 1, "Exact synthetic final response.")
}

func TestAtomicFinalizationUnknownAckConfirmsExactEffectsWithoutMutationRetry(t *testing.T) {
	store, repo, input := atomicFinalizationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	calls := 0
	run := func(ctx context.Context, fn func(*transcriptstore.ImmediateTransaction) error) (transcriptstore.ImmediateTransactionOutcome, error) {
		calls++
		outcome, err := repo.RunImmediateWithOutcome(ctx, fn)
		if err != nil {
			return outcome, err
		}
		if !outcome.Committed {
			t.Fatal("fixture did not really COMMIT")
		}
		outcome.Committed = false
		return outcome, errors.New("synthetic lost COMMIT acknowledgement")
	}
	result, outcome, err := store.finalizeRunnerTranscriptWithRunner(ctx, input, repo, run)
	if err != nil || calls != 1 || !outcome.ReadbackConfirmed || outcome.Transaction.Committed || !outcome.Transaction.CommitAttempted || result.EnqueuedMemory == nil || result.Aside == nil {
		t.Fatalf("unknown ACK not exactly confirmed: calls=%d result=%#v outcome=%#v err=%v", calls, result, outcome, err)
	}
	atomicFinalizationCounts(t, store, input, 1, "Exact synthetic final response.")
	wrong := input
	assistant := *input.Transcript.Assistant
	assistant.PayloadJSON = []byte(`{"text":"different response","assistant_segment":{"version":1,"ordinal":1}}`)
	wrong.Transcript.Assistant = &assistant
	if _, found, err := store.confirmRunnerTranscriptFinalization(ctx, wrong, result, repo); err == nil || found {
		t.Fatal("matching terminal status accepted a different final response")
	}
	wrongMemory := result
	copyMemory := *result.EnqueuedMemory
	copyMemory.Payload = []byte(`{}`)
	wrongMemory.EnqueuedMemory = &copyMemory
	if _, found, err := store.confirmRunnerTranscriptFinalization(ctx, input, wrongMemory, repo); err == nil || found {
		t.Fatal("matching pair accepted different Memory facts")
	}
	wrongAside := result
	wrongAside.Aside = &RunnerFinalizationAside{FrameID: result.Aside.FrameID, Payload: []byte(`{"response":"different"}`)}
	if _, found, err := store.confirmRunnerTranscriptFinalization(ctx, input, wrongAside, repo); err == nil || found {
		t.Fatal("matching pair accepted different Aside projection")
	}
	if err := store.SetFrameOutputData("frame", map[string]any{"response": "later response"}); err != nil {
		t.Fatal(err)
	}
	replayed, replayOutcome, err := store.FinalizeRunnerTranscript(ctx, input)
	if err != nil || !replayOutcome.KnownCommit() || !replayed.Replayed || replayed.Aside != nil || replayed.EnqueuedMemory != nil {
		t.Fatalf("old receipt replay created new effects: result=%#v outcome=%#v err=%v", replayed, replayOutcome, err)
	}
	atomicFinalizationCounts(t, store, input, 1, "later response")
	snapshot, err := repo.GetProjectionSnapshot(ctx, input.Transcript.Finish.Claim.StreamUID, input.Transcript.Finish.Claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: snapshot.StreamUID, OwnerID: input.Transcript.Finish.Claim.OwnerID, SourceBranchID: snapshot.BranchID,
		ExpectedActiveBranchID: snapshot.BranchID, ExpectedGeneration: snapshot.BranchGeneration,
		ClientMutationID: "new-branch", SourceClientMessageID: "task", SourceMessageIndex: 0,
		ReplacementText: "A separate synthetic question.", Destinations: []string{"ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ReadRunnerFinalization(ctx, input.Transcript, nil, nil); !errors.Is(err, transcriptstore.ErrBranchStateStale) || found {
		t.Fatalf("non-active old pair accepted: found=%t err=%v", found, err)
	}
}

func TestAtomicFinalizationFailedCommitRemainsUnknownAndHasNoEffects(t *testing.T) {
	store, repo, input := atomicFinalizationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	run := func(ctx context.Context, fn func(*transcriptstore.ImmediateTransaction) error) (transcriptstore.ImmediateTransactionOutcome, error) {
		return repo.RunImmediateWithOutcome(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
			if err := fn(tx); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO transcript_delivery_routes(stream_uid,destination,current_generation,status,updated_at)
				VALUES('missing-stream','ws',1,'active',CURRENT_TIMESTAMP)`)
			return err
		})
	}
	_, outcome, err := store.finalizeRunnerTranscriptWithRunner(ctx, input, repo, run)
	if err == nil || !outcome.Transaction.CommitAttempted || outcome.KnownCommit() {
		t.Fatalf("failed COMMIT was promoted: outcome=%#v err=%v", outcome, err)
	}
	atomicFinalizationCounts(t, store, input, 0, "previous response")
}
