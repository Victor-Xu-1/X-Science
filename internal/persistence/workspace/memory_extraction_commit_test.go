package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func newExtractionCommitFixture(t *testing.T) (*Store, string, ExtractionCommitInput) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "extraction-project", UserID: "extraction-owner", Name: "Extraction"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "extraction-frame", ProjectID: "extraction-project", AgentName: "GENERAL", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	var incarnation string
	if err := store.db.QueryRow(`SELECT incarnation_id FROM frames WHERE id='extraction-frame'`).Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	source := CompletedExtractionSource{
		Schema: 1, OwnerUserID: "extraction-owner", ProjectID: "extraction-project", FrameID: "extraction-frame",
		Legacy: &LegacyExtractionSource{SessionID: "extraction-frame", FrameIncarnationID: incarnation, FinishEventID: 3,
			PrefixSHA256: extractionDigest([]byte("strict completed journal prefix"))},
	}
	input := ExtractionCommitInput{Source: source, InputSHA256: extractionDigest([]byte("immutable completed messages")),
		CursorNext: 2, AdvanceCursor: true, DeltaCount: 2, Gate: "done",
		Plan: ExtractionMutationPlan{Append: []AgentMemoryAppendMutation{{Input: CreateMemoryInput{
			ID: "extracted-append", Body: "Reports preserve explicit random seeds.", Evidence: "stated", SubjectProjectID: source.ProjectID,
		}}}},
	}
	return store, path, input
}

func extractionFixtureMemory(t *testing.T, store *Store, id string) Memory {
	t.Helper()
	return mustCreateWorkspaceMemory(t, store, CreateMemoryInput{
		ID: id, UserID: "extraction-owner", Body: "Existing durable fact " + id, Evidence: "stated", Origin: "extractor",
		SubjectProjectID: "extraction-project", SourceFrameID: "extraction-frame",
	})
}

func assertExtractionCounts(t *testing.T, store *Store, memories, cursor, receipts int) {
	t.Helper()
	var actualMemories, actualReceipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&actualMemories); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM memory_extraction_receipts`).Scan(&actualReceipts); err != nil {
		t.Fatal(err)
	}
	actualCursor, err := store.LastExtractMessageIndex(t.Context(), "extraction-frame")
	if err != nil || actualMemories != memories || actualReceipts != receipts || actualCursor != cursor {
		t.Fatalf("atomic state memories=%d cursor=%d receipts=%d err=%v want=%d/%d/%d", actualMemories, actualCursor, actualReceipts, err, memories, cursor, receipts)
	}
}

func TestMemoryExtractionCommitRollsBackEveryAcceptedEffect(t *testing.T) {
	for _, boundary := range []string{"cursor", "receipt"} {
		t.Run(boundary, func(t *testing.T) {
			store, _, input := newExtractionCommitFixture(t)
			old, remove := extractionFixtureMemory(t, store, "predecessor"), extractionFixtureMemory(t, store, "removal")
			input.Plan.Replace = []ExtractionReplacement{{Expected: old, NewID: "successor", Body: old.Body + " with correction", Evidence: "observed"}}
			input.Plan.Remove = []ExtractionRemoval{{Expected: remove}}
			input.Plan.Prepared.Failures = []ExtractionOperationFailure{{Kind: "append", Error: "ordinary rejected operation"}}
			if _, err := store.SetLastExtractMessageIndex(t.Context(), input.Source.FrameID, 0, nil); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER fail_atomic_extraction BEFORE INSERT ON memory_extraction_receipts
			 BEGIN SELECT RAISE(ABORT,'injected extraction failure'); END`
			if boundary == "cursor" {
				trigger = `CREATE TRIGGER fail_atomic_extraction BEFORE UPDATE OF last_extract_msg_idx ON frame_runtime_metadata
				 BEGIN SELECT RAISE(ABORT,'injected extraction failure'); END`
			}
			if _, err := store.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			result, err := store.CommitMemoryExtraction(t.Context(), input)
			if err == nil || !strings.Contains(err.Error(), "injected extraction failure") || result.Gate != "" {
				t.Fatalf("aborted commit result=%+v err=%v", result, err)
			}
			assertExtractionCounts(t, store, 2, 0, 0)
			if row, found, err := store.GetMemoryOwned(t.Context(), input.Source.OwnerUserID, old.ID); err != nil || !found || row.SupersededBy != "" || row.Body != old.Body {
				t.Fatalf("rollback changed predecessor: row=%+v found=%t err=%v", row, found, err)
			}
			if _, err := store.db.Exec(`DROP TRIGGER fail_atomic_extraction`); err != nil {
				t.Fatal(err)
			}
			result, err = store.CommitMemoryExtraction(t.Context(), input)
			if err != nil || result.Replayed || !result.CursorAdvanced || result.InputSHA256 != input.InputSHA256 ||
				!reflect.DeepEqual(result.Applied.Appended, []string{"extracted-append"}) ||
				!reflect.DeepEqual(result.Applied.Replaced, []string{old.ID}) || !reflect.DeepEqual(result.Applied.Removed, []string{remove.ID}) ||
				len(result.Applied.Failures) != 1 {
				t.Fatalf("retry=%+v err=%v", result, err)
			}
			assertExtractionCounts(t, store, 3, 2, 1)
		})
	}
}

func TestMemoryExtractionReceiptReplaysAfterReopenAndPolicyChange(t *testing.T) {
	store, path, input := newExtractionCommitFixture(t)
	first, err := store.CommitMemoryExtraction(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE memory_extraction_receipts SET result_json='{}'`); err == nil {
		t.Fatal("committed extraction receipt was mutable")
	}
	if err := store.SetMemoryEnabled(t.Context(), input.Source.OwnerUserID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProjectMemoryEnabled(t.Context(), input.Source.ProjectID, input.Source.OwnerUserID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, found, err := reopened.ReadMemoryExtractionReceipt(t.Context(), input.Source)
	if err != nil || !found || !saved.Replayed {
		t.Fatalf("reopen read=%+v found=%t err=%v", saved, found, err)
	}
	saved.Replayed = false
	if !reflect.DeepEqual(first, saved) {
		t.Fatalf("replay lost original outcome: first=%+v saved=%+v", first, saved)
	}
	again, err := reopened.CommitMemoryExtraction(t.Context(), input)
	if err != nil || !again.Replayed || !reflect.DeepEqual(first.Applied, again.Applied) {
		t.Fatalf("reopen commit replay=%+v err=%v", again, err)
	}
	assertExtractionCounts(t, reopened, 1, 2, 1)

	conflict := input
	conflict.InputSHA256 = extractionDigest([]byte("different completed input"))
	if _, err := reopened.CommitMemoryExtraction(t.Context(), conflict); !errors.Is(err, ErrExtractionPlanConflict) {
		t.Fatalf("changed input error=%v", err)
	}
	conflict = input
	conflict.Plan.Append = append([]AgentMemoryAppendMutation(nil), input.Plan.Append...)
	conflict.Plan.Append[0].Input.Body = "different prepared plan"
	if _, err := reopened.CommitMemoryExtraction(t.Context(), conflict); !errors.Is(err, ErrExtractionPlanConflict) {
		t.Fatalf("changed plan error=%v", err)
	}
	assertExtractionCounts(t, reopened, 1, 2, 1)
}

func TestMemoryExtractionConcurrentSameSourceCommitsOnce(t *testing.T) {
	store, _, input := newExtractionCommitFixture(t)
	start := make(chan struct{})
	type response struct {
		result ExtractionCommitResult
		err    error
	}
	results := make(chan response, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			result, err := store.CommitMemoryExtraction(t.Context(), input)
			results <- response{result, err}
		})
	}
	close(start)
	group.Wait()
	close(results)
	fresh, replayed := 0, 0
	for response := range results {
		if response.err != nil {
			t.Fatal(response.err)
		}
		if response.result.Replayed {
			replayed++
		} else {
			fresh++
		}
	}
	if fresh != 1 || replayed != 1 {
		t.Fatalf("fresh=%d replayed=%d", fresh, replayed)
	}
	assertExtractionCounts(t, store, 1, 2, 1)
}

func canonicalExtractionCommitFixture(t *testing.T) (*Store, *transcriptstore.Repository, ExtractionCommitInput) {
	t.Helper()
	store, _, input := newExtractionCommitFixture(t)
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source := input.Source
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "extraction-stream", OwnerID: source.OwnerUserID, ExternalID: source.FrameID, SessionID: source.FrameID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: source.ProjectID, RootFrameID: source.FrameID, FrameID: source.FrameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "extraction-input", FrameEventID: "extraction-input-event",
		MessageUUID: "extraction-message", Text: "Preserve reproducibility preferences.",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "extraction-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	finish, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: claimed.Claim, ClientMessageID: "extraction-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := repo.ReadCompletedMemorySource(t.Context(), stream.UID, stream.OwnerID, finish.EventID)
	if err != nil {
		t.Fatal(err)
	}
	input.Source.Legacy, input.Source.Transcript = nil, &proof
	if err := repo.RunImmediate(context.Background(), func(tx *transcriptstore.ImmediateTransaction) error {
		inside, err := tx.ReadCompletedMemorySource(t.Context(), stream.UID, stream.OwnerID, finish.EventID)
		if err != nil {
			return err
		}
		if inside != proof {
			t.Fatal("transactional completed-source query diverged from repository reader")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store, repo, input
}
