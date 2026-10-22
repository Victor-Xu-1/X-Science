package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestMemoryExtractionOlderCompletionCannotResetNewerCommittedCursor(t *testing.T) {
	store, repo, older := canonicalExtractionCommitFixture(t)
	streamUID, owner := older.Source.Transcript.StreamUID, older.Source.OwnerUserID
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: streamUID, OwnerID: owner, ClientMessageID: "newer-input", FrameEventID: "newer-input-event",
		MessageUUID: "newer-message", Text: "Continue with a newer reproducibility preference.",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: streamUID, OwnerID: owner, RunnerID: "newer-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("newer claim=%+v err=%v", claimed, err)
	}
	finish, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: claimed.Claim, ClientMessageID: "newer-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := repo.ReadCompletedMemorySource(t.Context(), streamUID, owner, finish.EventID)
	if err != nil {
		t.Fatal(err)
	}
	newer := older
	newer.Source.Transcript = &proof
	newer.CursorNext, newer.DeltaCount = 4, 4
	newer.InputSHA256 = extractionDigest([]byte("newer immutable messages"))
	newer.Plan.Append = append([]AgentMemoryAppendMutation(nil), older.Plan.Append...)
	newer.Plan.Append[0].Input.ID = "newer-append"
	if _, err := store.CommitMemoryExtraction(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	assertExtractionOrderingQueryIsIndexed(t, store, older)
	// A delayed older completion observes B's cursor above its own snapshot
	// length. This is not branch-truncation recovery and must not rewind B.
	older.CursorBefore, older.Overshoot = 4, true
	if _, err := store.CommitMemoryExtraction(t.Context(), older); !errors.Is(err, ErrExtractionSourceChanged) {
		t.Fatalf("older completion was not fenced: err=%v", err)
	}
	assertExtractionCounts(t, store, 1, 4, 1)
}

func assertExtractionOrderingQueryIsIndexed(t *testing.T, store *Store, input ExtractionCommitInput) {
	t.Helper()
	proof := input.Source.Transcript
	rows, err := store.db.Query(`EXPLAIN QUERY PLAN `+newerExtractionReceiptQuery, proof.StreamUID, proof.BranchID,
		input.Source.FrameID, input.Source.OwnerUserID, proof.FrameIncarnationID, proof.StreamUID, proof.StreamEpoch, proof.PublicationSeq)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "USING INDEX memory_extraction_receipts_frame_idx") || strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("ordering lookup did not use bounded receipt index: %s", plan)
	}
}
