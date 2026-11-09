package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"sync/atomic"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureStudyBatchEmptyReadContract(t *testing.T) {
	f, _, _ := serverStudyFixture(t)
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/study-batch", nil, f.f.stream.OwnerID)
	var view struct {
		Schema      string          `json:"schema"`
		Limit       int             `json:"limit"`
		Concurrency int             `json:"concurrency"`
		Batch       json.RawMessage `json:"batch"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &view) != nil ||
		view.Schema != "x-science.literature-study-batch.v1" || view.Limit != 5 || view.Concurrency != 2 || string(view.Batch) != "null" {
		t.Fatalf("authenticated batch checkpoint read is not connected: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLiteratureStudyBatchWaveReconcilesCommittedRows(t *testing.T) {
	f, _, _ := serverStudyFixture(t)
	s := f.f.server
	store := f.f.store
	owner := f.f.stream.OwnerID
	second, err := store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Second synthetic paper", Metadata: map[string]any{"abstract": "Another synthetic association."}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.ControlLiteratureStudyBatch(t.Context(), owner, workspace.LiteratureStudyBatchCommand{Action: "start", OperationID: uuid.NewString(), Language: "en-US", ItemIDs: []string{f.itemID, second.Item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	infer := func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
		calls.Add(1)
		question := "A bounded synthetic overview"
		return workspace.LiteratureStudySections{Question: &question}, "no-network", nil
	}
	evidence := func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
		return literatureStudyEvidence{}, errors.New("use synthetic abstract")
	}
	command := workspace.LiteratureStudyBatchCommand{Action: "advance", BatchID: batch.ID, Rows: []int{0, 1}}
	result, err := s.advanceLiteratureStudyBatch(t.Context(), owner, command, evidence, infer)
	if err != nil || calls.Load() != 2 || result.Rows[0].State != "succeeded" || result.Rows[1].State != "succeeded" {
		t.Fatal(result, err, calls.Load())
	}
	if _, err := s.advanceLiteratureStudyBatch(t.Context(), owner, command, evidence, infer); err != nil || calls.Load() != 2 {
		t.Fatal("lost response reinferred", err, calls.Load())
	}
}

func TestLiteratureStudyBatchCancellationRetainsSibling(t *testing.T) {
	f, _, _ := serverStudyFixture(t)
	s := f.f.server
	store := f.f.store
	owner := f.f.stream.OwnerID
	second, err := store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Pending sibling", Metadata: map[string]any{"abstract": "Synthetic pending evidence."}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.ControlLiteratureStudyBatch(t.Context(), owner, workspace.LiteratureStudyBatchCommand{Action: "start", OperationID: uuid.NewString(), Language: "en-US", ItemIDs: []string{f.itemID, second.Item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// Complete the first row before the controlled cancellation, without a sleep.
	evidence := func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
		return literatureStudyEvidence{}, nil
	}
	infer := func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
		return workspace.LiteratureStudySections{}, "synthetic", nil
	}
	if _, err := s.advanceLiteratureStudyBatch(t.Context(), owner, workspace.LiteratureStudyBatchCommand{Action: "advance", BatchID: batch.ID, Rows: []int{0}}, evidence, infer); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = s.advanceLiteratureStudyBatch(ctx, owner, workspace.LiteratureStudyBatchCommand{Action: "advance", BatchID: batch.ID, Rows: []int{1}}, evidence, func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
		cancel()
		return workspace.LiteratureStudySections{}, "cancelled", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	read, err := store.ReadLiteratureStudyBatch(t.Context(), owner)
	if err != nil || !read.Paused || read.Rows[0].State != "succeeded" || read.Rows[1].State != "pending" || read.RequestExpiresAt != 0 {
		t.Fatal(read, err)
	}
	// A failed sibling pauses the batch without discarding the committed row.
	resume, err := store.ControlLiteratureStudyBatch(t.Context(), owner, workspace.LiteratureStudyBatchCommand{Action: "resume", OperationID: uuid.NewString(), BatchID: batch.ID, ExpectedRevision: read.Revision})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.advanceLiteratureStudyBatch(t.Context(), owner, workspace.LiteratureStudyBatchCommand{Action: "advance", BatchID: resume.ID, Rows: []int{1}}, evidence, func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
		return workspace.LiteratureStudySections{}, "", errors.New("synthetic failure")
	})
	if err != nil || !failed.Paused || failed.Rows[0].State != "succeeded" || failed.Rows[1].State != "failed" {
		t.Fatal(failed, err)
	}
}
