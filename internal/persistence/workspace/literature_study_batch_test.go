package workspace

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"path/filepath"
	"testing"
	"time"
)

func startStudyBatchFixture(t *testing.T, s *Store, count int) (*LiteratureStudyBatch, []LiteratureStudySource, LiteratureStudyBatchCommand) {
	t.Helper()
	var sources []LiteratureStudySource
	command := LiteratureStudyBatchCommand{Action: "start", OperationID: uuid.NewString(), Language: "en-US"}
	for i := 0; i < count; i++ {
		source, _ := studyTestItem(t, s)
		sources = append(sources, source)
		command.ItemIDs = append(command.ItemIDs, source.ItemID)
	}
	batch, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", command)
	if err != nil {
		t.Fatal(err)
	}
	return batch, sources, command
}
func claimStudyBatchFixture(t *testing.T, s *Store, batch *LiteratureStudyBatch, rows ...int) LiteratureStudyBatchClaim {
	t.Helper()
	claim, _, err := s.ClaimLiteratureStudyBatch(t.Context(), "study-owner", LiteratureStudyBatchCommand{Action: "advance", BatchID: batch.ID, Rows: rows}, time.Minute)
	if err != nil || claim.Token == "" {
		t.Fatal(claim, err)
	}
	return claim
}

func TestLiteratureStudyBatchPauseDoesNotInvalidateCurrentWave(t *testing.T) {
	s := candidateTestStore(t)
	batch, sources, _ := startStudyBatchFixture(t, s, 2)
	claim := claimStudyBatchFixture(t, s, batch, 0, 1)
	if _, _, err := s.ClaimLiteratureStudyBatch(t.Context(), "study-owner", LiteratureStudyBatchCommand{Action: "advance", BatchID: batch.ID, Rows: []int{1}}, time.Minute); !errors.Is(err, ErrLiteratureStudyBatchBusy) {
		t.Fatal("duplicate wave admitted", err)
	}
	current, _ := s.ReadLiteratureStudyBatch(t.Context(), "study-owner")
	paused, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", LiteratureStudyBatchCommand{Action: "pause", OperationID: uuid.NewString(), BatchID: batch.ID, ExpectedRevision: current.Revision})
	if err != nil || !paused.Paused {
		t.Fatal(paused, err)
	}
	for index := range sources {
		if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", claim, index, studyTestSnapshot(sources[index])); err != nil {
			t.Fatal("pause invalidated legitimate token", err)
		}
	}
	if err := s.FinishLiteratureStudyBatch(t.Context(), "study-owner", claim, false); err != nil {
		t.Fatal(err)
	}
	result, err := s.ReadLiteratureStudyBatchResult(t.Context(), "study-owner", batch.ID, 1)
	if err != nil || result.Revision != 1 || result.Snapshot == nil {
		t.Fatal(result, err)
	}
}

func TestLiteratureStudyBatchCancelReopenAndExactResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	batch, sources, _ := startStudyBatchFixture(t, s, 2)
	claim := claimStudyBatchFixture(t, s, batch, 0, 1)
	if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", claim, 0, studyTestSnapshot(sources[0])); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishLiteratureStudyBatch(t.Context(), "study-owner", claim, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	read, err := s.ReadLiteratureStudyBatch(t.Context(), "study-owner")
	if err != nil || !read.Paused || read.Rows[0].State != "succeeded" || read.Rows[1].State != "pending" || read.RequestExpiresAt != 0 {
		t.Fatal(read, err)
	}
	resumed, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", LiteratureStudyBatchCommand{Action: "resume", OperationID: uuid.NewString(), BatchID: batch.ID, ExpectedRevision: read.Revision})
	if err != nil {
		t.Fatal(err)
	}
	next := claimStudyBatchFixture(t, s, resumed, 0, 1)
	if len(next.Rows) != 1 || next.Rows[0] != 1 {
		t.Fatal("committed sibling scheduled again", next)
	}
	if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", next, 1, studyTestSnapshot(sources[1])); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishLiteratureStudyBatch(t.Context(), "study-owner", next, false); err != nil {
		t.Fatal(err)
	}
	// A later same-source regeneration must not replace the frozen batch result.
	input := batch.Rows[0].Request
	input.OperationID = uuid.NewString()
	input.ExpectedRevision = 1
	changed := studyTestSnapshot(sources[0])
	question := "Later unrelated snapshot"
	changed.Sections.Question = &question
	if _, err := s.PublishLiteratureStudy(t.Context(), "study-owner", sources[0].ItemID, input, changed); err != nil {
		t.Fatal(err)
	}
	result, err := s.ReadLiteratureStudyBatchResult(t.Context(), "study-owner", batch.ID, 0)
	if err != nil || result.Revision != 1 || result.Snapshot == nil || *result.Snapshot.Sections.Question == question {
		t.Fatal("result reference drifted", result, err)
	}
}

func TestLiteratureStudyBatchExpiredClaimAndOldReceiptFences(t *testing.T) {
	s := candidateTestStore(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	batch, sources, start := startStudyBatchFixture(t, s, 1)
	old := claimStudyBatchFixture(t, s, batch, 0)
	now = now.Add(2 * time.Minute)
	current := claimStudyBatchFixture(t, s, batch, 0)
	if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", old, 0, studyTestSnapshot(sources[0])); !errors.Is(err, ErrLiteratureStudyBatchClaim) {
		t.Fatal("old token published", err)
	}
	_, view, err := s.ReadLiteratureStudy(t.Context(), "study-owner", sources[0].ItemID, "en-US")
	if err != nil || view.Revision != 0 {
		t.Fatal("old token left snapshot or receipt", view, err)
	}
	if err := s.FinishLiteratureStudyBatch(t.Context(), "study-owner", old, true); !errors.Is(err, ErrLiteratureStudyBatchClaim) {
		t.Fatal("old cleanup cleared current claim", err)
	}
	if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", current, 0, studyTestSnapshot(sources[0])); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishLiteratureStudyBatch(t.Context(), "study-owner", current, false); err != nil {
		t.Fatal(err)
	}
	newBatch, _, _ := startStudyBatchFixture(t, s, 1)
	if _, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", start); !errors.Is(err, ErrLiteratureStudyBatchConflict) {
		t.Fatal("old start receipt revived batch", err)
	}
	read, _ := s.ReadLiteratureStudyBatch(t.Context(), "study-owner")
	if read.ID != newBatch.ID {
		t.Fatal("new batch overwritten")
	}
}

func TestLiteratureStudyBatchReuseSourceOwnerAndBounds(t *testing.T) {
	s := candidateTestStore(t)
	source, input := studyTestItem(t, s)
	if _, err := s.PublishLiteratureStudy(t.Context(), "study-owner", source.ItemID, input, studyTestSnapshot(source)); err != nil {
		t.Fatal(err)
	}
	batch, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", LiteratureStudyBatchCommand{Action: "start", OperationID: uuid.NewString(), Language: "en-US", ItemIDs: []string{source.ItemID}})
	if err != nil || !batch.Rows[0].Reused || batch.Rows[0].State != "succeeded" {
		t.Fatal(batch, err)
	}
	if _, err := s.ReadLiteratureStudyBatchResult(t.Context(), "foreign-owner", batch.ID, 0); !errors.Is(err, ErrLiteratureStudyBatchConflict) {
		t.Fatal("foreign result", err)
	}
	title := "Changed source"
	if _, err := s.PatchLiteratureItem(t.Context(), "study-owner", source.ItemID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Title: &title}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadLiteratureStudyBatchResult(t.Context(), "study-owner", batch.ID, 0); !errors.Is(err, ErrLiteratureStudySourceChanged) {
		t.Fatal("old source exposed", err)
	}
	bad := LiteratureStudyBatchCommand{Action: "start", OperationID: uuid.NewString(), Language: "en-US", ItemIDs: []string{source.ItemID, source.ItemID}}
	if _, err := s.ControlLiteratureStudyBatch(context.Background(), "study-owner", bad); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatal("duplicate rows admitted", err)
	}
}

func TestLiteratureStudyBatchRetainsSinglePublicationPreflight(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var closed *Store
	if _, err := closed.PublishLiteratureStudy(ctx, "", "", LiteratureStudyGenerate{}, LiteratureStudySnapshot{}); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatal("invalid legacy publication entered transaction", err)
	}
}

func TestLiteratureStudyBatchCompletedClaimExpiryAllowsNextBatch(t *testing.T) {
	s := candidateTestStore(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	batch, sources, _ := startStudyBatchFixture(t, s, 1)
	claim := claimStudyBatchFixture(t, s, batch, 0)
	if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", claim, 0, studyTestSnapshot(sources[0])); err != nil {
		t.Fatal(err)
	}
	// The publication committed; the request died before its Finish write.
	nextSource, _ := studyTestItem(t, s)
	next := LiteratureStudyBatchCommand{Action: "start", OperationID: uuid.NewString(), Language: "en-US", ItemIDs: []string{nextSource.ItemID}}
	if _, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", next); !errors.Is(err, ErrLiteratureStudyBatchBusy) {
		t.Fatal("live finished wave lost protection", err)
	}
	now = now.Add(2 * time.Minute)
	newBatch, err := s.ControlLiteratureStudyBatch(t.Context(), "study-owner", next)
	if err != nil {
		t.Fatalf("completed checkpoint stays busy after expired Finish: %v", err)
	}
	if newBatch.ID == batch.ID {
		t.Fatal("new batch not admitted")
	}
	if err := s.FinishLiteratureStudyBatch(t.Context(), "study-owner", claim, true); !errors.Is(err, ErrLiteratureStudyBatchClaim) {
		t.Fatal("late Finish touched next batch", err)
	}
	if _, err := s.PublishLiteratureStudyBatchRow(t.Context(), "study-owner", claim, 0, studyTestSnapshot(sources[0])); !errors.Is(err, ErrLiteratureStudyBatchClaim) {
		t.Fatal("late Publish touched next batch", err)
	}
	if replay, found, err := s.ReplayLiteratureStudy(t.Context(), "study-owner", sources[0].ItemID, batch.Rows[0].Request); err != nil || !found || replay.Revision != 1 {
		t.Fatal("committed child receipt lost", replay, found, err)
	}
}
