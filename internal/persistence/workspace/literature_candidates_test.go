package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"path/filepath"
	"reflect"
	"testing"
)

func candidateTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func candidateInput() LiteratureCandidateStage {
	return LiteratureCandidateStage{OperationID: uuid.NewString(), Provider: "semantic-scholar", ExternalID: "paper-a", Title: "Synthetic A", Metadata: map[string]any{"doi": "https://doi.org/10.1234/EXAMPLE", "pmid": "12345"}}
}

func TestLiteratureCandidateRestorePreservesDurableOriginalReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	in := candidateInput()
	in.Metadata["precise"] = json.Number("9007199254740993")
	staged, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	id := staged.Candidate.ID
	dismissal := LiteratureCandidateAction{OperationID: uuid.NewString()}
	dismissed, err := s.DecideLiteratureCandidate(ctx, "owner", id, "dismiss", dismissal)
	if err != nil {
		t.Fatal(err)
	}
	restore := LiteratureCandidateAction{OperationID: uuid.NewString()}
	restored, err := s.DecideLiteratureCandidate(ctx, "owner", id, "restore", restore)
	if err != nil || restored.Action != "restore" || restored.Candidate.State != "pending" || restored.Candidate.ItemID != "" || !reflect.DeepEqual(restored.Candidate.Metadata, dismissed.Candidate.Metadata) {
		t.Fatalf("restore to pending without changing metadata: %#v %v", restored, err)
	}
	if _, err = s.DecideLiteratureCandidate(ctx, "owner", id, "restore", LiteratureCandidateAction{OperationID: uuid.NewString()}); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("new restore must require dismissed state: %v", err)
	}
	if _, err = s.DecideLiteratureCandidate(ctx, "other-owner", id, "restore", restore); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign restore must not expose owner receipt: %v", err)
	}
	if _, err = s.DecideLiteratureCandidate(ctx, "owner", "another-candidate", "restore", restore); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("restore operation cannot change candidate identity: %v", err)
	}
	items, err := s.ListLiteratureItems(ctx, "owner", "", "", "", 20)
	if err != nil || len(items.Items) != 0 {
		t.Fatalf("restore must not accept or create a Library item: %#v %v", items, err)
	}
	accept := LiteratureCandidateAction{OperationID: uuid.NewString()}
	accepted, err := s.DecideLiteratureCandidate(ctx, "owner", id, "accept", accept)
	if err != nil || accepted.Candidate.State != "accepted" {
		t.Fatalf("accept restored candidate: %#v %v", accepted, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.DecideLiteratureCandidate(ctx, "owner", id, "restore", restore)
	if err != nil || !reflect.DeepEqual(replay, restored) {
		t.Fatalf("reopened original restore must replay before current accepted-state check: %#v %v", replay, err)
	}
	replay, err = s.DecideLiteratureCandidate(ctx, "owner", id, "dismiss", dismissal)
	if err != nil || !reflect.DeepEqual(replay, dismissed) {
		t.Fatalf("restore must not rewrite the older dismissal receipt: %#v %v", replay, err)
	}
	if _, err = s.DecideLiteratureCandidate(ctx, "owner", id, "restore", LiteratureCandidateAction{OperationID: uuid.NewString()}); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("accepted candidate cannot be restored: %v", err)
	}
	page, err := s.ListLiteratureCandidates(ctx, "owner", "dismissed", "")
	if err != nil || len(page.Candidates) != 0 {
		t.Fatalf("settled candidate must not remain in dismissed readback: %#v %v", page, err)
	}
}

func TestLiteratureCandidateDurableLifecycleAndLedger(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	in := candidateInput()
	in.Metadata["precise"] = json.Number("9007199254740993")
	first, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil || first.Candidate.State != "pending" {
		t.Fatalf("stage: %#v %v", first, err)
	}
	again, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("frozen replay: %#v %v", again, err)
	}
	bad := in
	bad.Title = "different"
	if _, err = s.StageLiteratureCandidate(ctx, "owner", bad); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("id reuse: %v", err)
	}
	dup := in
	dup.OperationID = uuid.NewString()
	dup.ExternalID = "paper-b"
	dup.Metadata = map[string]any{"pmid": "PMID: 12345"}
	result, err := s.StageLiteratureCandidate(ctx, "owner", dup)
	if err != nil || result.Candidate.ID != first.Candidate.ID {
		t.Fatalf("PMID duplicate: %#v %v", result, err)
	}
	if _, err = s.DecideLiteratureCandidate(ctx, "another-owner", first.Candidate.ID, "accept", LiteratureCandidateAction{OperationID: uuid.NewString()}); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign owner: %v", err)
	}
	dismissal := LiteratureCandidateAction{OperationID: uuid.NewString()}
	dismissed, err := s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "dismiss", dismissal)
	if err != nil || dismissed.Candidate.State != "dismissed" {
		t.Fatalf("dismiss: %#v %v", dismissed, err)
	}
	page, err := s.ListLiteratureCandidates(ctx, "owner", "dismissed", "")
	if err != nil || len(page.Candidates) != 1 {
		t.Fatalf("dismiss readback: %#v %v", page, err)
	}
	accept := LiteratureCandidateAction{OperationID: uuid.NewString()}
	accepted, err := s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "accept", accept)
	if err != nil || accepted.Candidate.State != "accepted" || accepted.Candidate.ItemID == "" {
		t.Fatalf("accept: %#v %v", accepted, err)
	}
	if _, err := s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "dismiss", LiteratureCandidateAction{OperationID: uuid.NewString()}); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("accepted candidate dismiss must conflict: %v", err)
	}
	item, err := s.GetLiteratureItem(ctx, "owner", accepted.Candidate.ItemID)
	if err != nil || item.Title != "Synthetic A" || len(item.Attachments) != 0 {
		t.Fatalf("library item: %#v %v", item, err)
	}
	if _, err = s.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Synthetic"}); err != nil {
		t.Fatal(err)
	}
	linked, err := s.SetLiteratureProject(ctx, "owner", accepted.Candidate.ItemID, "project", uuid.NewString(), true)
	if err != nil || len(linked.Item.ProjectIDs) != 1 || linked.Item.ProjectIDs[0] != "project" || len(linked.Item.Attachments) != 0 {
		t.Fatalf("metadata-only membership: %#v %v", linked, err)
	}
	pageItems, err := s.ListLiteratureItems(ctx, "owner", "Synthetic A", "project", "", 20)
	if err != nil || len(pageItems.Items) != 1 || pageItems.Items[0].ID != accepted.Candidate.ItemID {
		t.Fatalf("metadata-only query: %#v %v", pageItems, err)
	}
	replay, err := s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "dismiss", dismissal)
	if err != nil || !reflect.DeepEqual(replay, dismissed) {
		t.Fatalf("old receipt changed: %#v %v", replay, err)
	}
	replay, err = s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "accept", accept)
	if err != nil || !reflect.DeepEqual(replay, accepted) {
		t.Fatalf("accept replay: %#v %v", replay, err)
	}
	if _, err = s.db.Exec(`UPDATE literature_items SET deleted_at=? WHERE id=?`, s.now().UTC(), accepted.Candidate.ItemID); err != nil {
		t.Fatal(err)
	}
	later := in
	later.OperationID = uuid.NewString()
	if _, err = s.StageLiteratureCandidate(ctx, "owner", later); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("deleted accepted item mistaken as available: %v", err)
	}
	replay, err = s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "accept", accept)
	if err != nil || !reflect.DeepEqual(replay, accepted) {
		t.Fatalf("frozen replay after deletion: %#v %v", replay, err)
	}

}
func TestLiteratureCandidateLinksUniqueExistingOrRejectsAmbiguous(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	now := s.now().UTC()
	_, err := s.db.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "existing", "owner", "Existing", `{"pmid":"12345"}`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	in := candidateInput()
	r, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil || r.Candidate.State != "accepted" || r.Candidate.ItemID != "existing" {
		t.Fatalf("unique existing: %#v %v", r, err)
	}
	_, err = s.db.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "duplicate", "owner", "Duplicate", `{"pmid":"12345"}`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	in.OperationID = uuid.NewString()
	in.ExternalID = "another"
	in.Metadata = map[string]any{"pmid": "12345"}
	_, err = s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil {
		t.Fatalf("existing candidate receipt: %v", err)
	}
	_, err = s.db.Exec(`DELETE FROM literature_candidates`)
	if err != nil {
		t.Fatal(err)
	}
	in.OperationID = uuid.NewString()
	if _, err = s.StageLiteratureCandidate(ctx, "owner", in); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("ambiguous library: %v", err)
	}
}
func TestLiteratureCandidateIdentityConflictsAreNotSilentlyMerged(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	now := s.now().UTC()
	_, err := s.db.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "existing", "owner", "Existing", `{"doi":"invalid","pmid":"12345"}`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	in := candidateInput()
	r, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil || r.Candidate.State != "accepted" || r.Candidate.ItemID != "existing" {
		t.Fatalf("valid PMID lost because existing DOI malformed: %#v %v", r, err)
	}
	bad := in
	bad.OperationID = uuid.NewString()
	bad.Metadata = map[string]any{"doi": "10.1234/conflicting"}
	if _, err = s.StageLiteratureCandidate(ctx, "owner", bad); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("contradictory source identity: %v", err)
	}
}

func TestLiteratureCandidatePaginationAndStateScope(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	now := s.now().UTC()
	for n := 0; n < 101; n++ {
		id := fmt.Sprintf("candidate-%03d", n)
		_, err := s.db.Exec(`INSERT INTO literature_candidates(id,owner_user_id,provider,external_id,title,metadata_json,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',?,?)`, id, "owner", "provider", id, id, "{}", now, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.ListLiteratureCandidates(ctx, "owner", "pending", "")
	if err != nil || len(p.Candidates) != 100 || p.NextCursor == "" {
		t.Fatalf("first page %#v %v", p, err)
	}
	q, err := s.ListLiteratureCandidates(ctx, "owner", "pending", p.NextCursor)
	if err != nil || len(q.Candidates) != 1 || q.NextCursor != "" {
		t.Fatalf("second page %#v %v", q, err)
	}
	foreign, err := s.ListLiteratureCandidates(ctx, "other", "pending", "")
	if err != nil || len(foreign.Candidates) != 0 {
		t.Fatalf("foreign list %#v %v", foreign, err)
	}
}

func TestLiteratureCandidateConcurrentStageConverges(t *testing.T) {
	s := candidateTestStore(t)
	const n = 8
	type result struct {
		receipt LiteratureCandidateReceipt
		err     error
	}
	done := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			in := candidateInput()
			in.OperationID = uuid.NewString()
			r, e := s.StageLiteratureCandidate(context.Background(), "owner", in)
			done <- result{r, e}
		}()
	}
	id := ""
	for i := 0; i < n; i++ {
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		if id == "" {
			id = r.receipt.Candidate.ID
		}
		if r.receipt.Candidate.ID != id {
			t.Fatalf("concurrent stage split candidate identity: %s vs %s", id, r.receipt.Candidate.ID)
		}
	}
}

func TestLiteratureCandidateRetainsNewIdentifiersAcrossDuplicateSources(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	first := candidateInput()
	first.Metadata = map[string]any{"pmid": "12345"}
	a, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil {
		t.Fatal(err)
	}
	second := candidateInput()
	second.ExternalID = "paper-b"
	b, err := s.StageLiteratureCandidate(ctx, "owner", second)
	if err != nil || a.Candidate.ID != b.Candidate.ID {
		t.Fatalf("same PMID: %#v %v", b, err)
	}
	third := candidateInput()
	third.ExternalID = "paper-c"
	third.Metadata = map[string]any{"doi": "10.1234/example"}
	c, err := s.StageLiteratureCandidate(ctx, "owner", third)
	if err != nil || a.Candidate.ID != c.Candidate.ID {
		t.Fatalf("retained DOI: %#v %v", c, err)
	}
	replay, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil || !reflect.DeepEqual(replay, a) {
		t.Fatalf("original receipt changed: %#v %v", replay, err)
	}
}

func TestLiteratureCandidateAdditionalIdentifierCannotMergeDifferentRecords(t *testing.T) {
	ctx := context.Background()
	s := candidateTestStore(t)
	first := candidateInput()
	first.Metadata = map[string]any{"pmid": "111"}
	a, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil {
		t.Fatal(err)
	}
	second := candidateInput()
	second.ExternalID = "other"
	second.Metadata = map[string]any{"doi": "10.1234/other"}
	if _, err = s.StageLiteratureCandidate(ctx, "owner", second); err != nil {
		t.Fatal(err)
	}
	bridge := candidateInput()
	bridge.ExternalID = "third"
	bridge.Metadata = map[string]any{"pmid": "111", "doi": "10.1234/other"}
	if _, err = s.StageLiteratureCandidate(ctx, "owner", bridge); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("two candidate bridge: %v", err)
	}
	_, err = s.db.Exec("DELETE FROM literature_candidates WHERE external_id=?", second.ExternalID)
	if err != nil {
		t.Fatal(err)
	}
	now := s.now().UTC()
	_, err = s.db.Exec("INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)", "other-item", "owner", "Other", `{"doi":"10.1234/other"}`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	bridge.OperationID = uuid.NewString()
	if _, err = s.StageLiteratureCandidate(ctx, "owner", bridge); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("candidate to other Library bridge: %v", err)
	}
	replay, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil || !reflect.DeepEqual(replay, a) {
		t.Fatalf("original receipt changed: %#v %v", replay, err)
	}
}

func TestLiteratureCandidateProviderIDCaseDoesNotCreateDuplicate(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	a := candidateInput()
	a.Metadata = map[string]any{}
	a.ExternalID = "abcdef"
	first, err := s.StageLiteratureCandidate(ctx, "owner", a)
	if err != nil {
		t.Fatal(err)
	}
	a.OperationID = uuid.NewString()
	a.ExternalID = "ABCDEF"
	second, err := s.StageLiteratureCandidate(ctx, "owner", a)
	if err != nil || second.Candidate.ID != first.Candidate.ID {
		t.Fatalf("provider identity split: %#v %v", second, err)
	}
}

func TestLiteratureCandidatePersistsAllSourceAliases(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	first := candidateInput()
	first.Metadata = map[string]any{"doi": "10.1234/alias"}
	first.Provider = "source-a"
	first.ExternalID = "id-x"
	a, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil {
		t.Fatal(err)
	}
	second := candidateInput()
	second.Provider = "source-b"
	second.ExternalID = "id-y"
	second.Metadata = map[string]any{"doi": "10.1234/alias"}
	b, err := s.StageLiteratureCandidate(ctx, "owner", second)
	if err != nil || b.Candidate.ID != a.Candidate.ID {
		t.Fatalf("source bridge: %#v %v", b, err)
	}
	third := second
	third.OperationID = uuid.NewString()
	third.Metadata = map[string]any{}
	c, err := s.StageLiteratureCandidate(ctx, "owner", third)
	if err != nil || c.Candidate.ID != a.Candidate.ID {
		t.Fatalf("persisted alias: %#v %v", c, err)
	}
	fourth := third
	fourth.OperationID = uuid.NewString()
	fourth.Metadata = map[string]any{"doi": "10.1234/conflict"}
	if _, err = s.StageLiteratureCandidate(ctx, "owner", fourth); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("conflicting alias: %v", err)
	}
}
func TestLiteratureCandidateIgnoresUnrelatedAmbiguousHistoricMetadata(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	now := s.now().UTC()
	_, err := s.db.Exec("INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)", "old", "owner", "Old", `{"identifiers":[{"scheme":"doi","value":"10.1234/one"},{"scheme":"doi","value":"10.1234/two"}]}`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := candidateInput()
	unrelated.Metadata = map[string]any{"pmid": "456"}
	a, err := s.StageLiteratureCandidate(ctx, "owner", unrelated)
	if err != nil || a.Candidate.State != "pending" {
		t.Fatalf("unrelated legacy identity blocked: %#v %v", a, err)
	}
	related := candidateInput()
	related.ExternalID = "other"
	related.Metadata = map[string]any{"doi": "10.1234/one"}
	if _, err = s.StageLiteratureCandidate(ctx, "owner", related); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("related ambiguous identity accepted: %v", err)
	}
}

func TestLiteratureCandidateRestageReconcilesNewLibraryItemButKeepsDismissal(t *testing.T) {
	ctx := context.Background()
	s := candidateTestStore(t)
	first := candidateInput()
	first.Metadata = map[string]any{"doi": "10.1234/new"}
	a, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil {
		t.Fatal(err)
	}
	other := candidateInput()
	other.ExternalID = "dismissed"
	other.Metadata = map[string]any{"pmid": "888"}
	d, err := s.StageLiteratureCandidate(ctx, "owner", other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideLiteratureCandidate(ctx, "owner", d.Candidate.ID, "dismiss", LiteratureCandidateAction{OperationID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	now := s.now().UTC()
	for _, v := range []struct{ id, metadata string }{{"linked", `{"doi":"10.1234/new"}`}, {"dismissed-item", `{"pmid":"888"}`}} {
		if _, err = s.db.Exec("INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)", v.id, "owner", v.id, v.metadata, now, now); err != nil {
			t.Fatal(err)
		}
	}
	retry := first
	retry.OperationID = uuid.NewString()
	retry.Metadata = map[string]any{}
	b, err := s.StageLiteratureCandidate(ctx, "owner", retry)
	if err != nil || b.Candidate.State != "accepted" || b.Candidate.ItemID != "linked" {
		t.Fatalf("restage current Library: %#v %v", b, err)
	}
	replay, err := s.StageLiteratureCandidate(ctx, "owner", first)
	if err != nil || !reflect.DeepEqual(replay, a) {
		t.Fatalf("old stage receipt changed: %#v %v", replay, err)
	}
	other.OperationID = uuid.NewString()
	other.Metadata = map[string]any{}
	kept, err := s.StageLiteratureCandidate(ctx, "owner", other)
	if err != nil || kept.Candidate.State != "dismissed" {
		t.Fatalf("dismissal lost: %#v %v", kept, err)
	}
}

func TestLiteratureCandidateDiscoverySourceOwnerRevisionAndReplay(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	now := s.now().UTC()
	_, err := s.db.Exec("INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)", "seed", "owner", "Seed", "{}", now, now)
	if err != nil {
		t.Fatal(err)
	}
	in := candidateInput()
	in.Source = &LiteratureCandidateDiscoverySource{ItemID: "seed", MetadataRevision: 1, Direction: "references"}
	first, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE literature_items SET metadata_revision=2 WHERE id=?", "seed"); err != nil {
		t.Fatal(err)
	}
	replay, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("original receipt: %#v %v", replay, err)
	}
	later := in
	later.OperationID = uuid.NewString()
	if _, err = s.StageLiteratureCandidate(ctx, "owner", later); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("stale seed accepted: %v", err)
	}
	later.OperationID = uuid.NewString()
	if _, err = s.StageLiteratureCandidate(ctx, "other", later); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign seed accepted: %v", err)
	}
}
