package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureCandidateAcceptReconcilesPersistedAliases(t *testing.T) {
	for _, mode := range []string{"unique", "foreign-owner", "conflicting-identifier", "ambiguous", "restage"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx := candidateTestStore(t), context.Background()
			first := candidateInput()
			first.Metadata = map[string]any{"doi": "10.1234/alias"}
			a, err := s.StageLiteratureCandidate(ctx, "owner", first)
			if err != nil {
				t.Fatal(err)
			}
			second := first
			second.OperationID, second.ExternalID = uuid.NewString(), "paper-b"
			b, err := s.StageLiteratureCandidate(ctx, "owner", second)
			if err != nil || a.Candidate.ID != b.Candidate.ID {
				t.Fatalf("stage alias: %#v %v", b, err)
			}
			insert := func(id, owner, metadata string) {
				t.Helper()
				now := s.now().UTC()
				if _, err := s.db.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, owner, id, metadata, now, now); err != nil {
					t.Fatal(err)
				}
			}
			insert("foreign", "another-owner", `{"semantic_scholar_id":"paper-b","doi":"10.1234/other"}`)
			if mode != "foreign-owner" {
				metadata := `{"semantic_scholar_id":"paper-b"}`
				if mode == "conflicting-identifier" {
					metadata = `{"semantic_scholar_id":"paper-b","doi":"10.1234/other"}`
				}
				insert("existing-b", "owner", metadata)
			}
			if mode == "ambiguous" {
				insert("existing-a", "owner", `{"semantic_scholar_id":"paper-a"}`)
			}
			var result LiteratureCandidateReceipt
			if mode == "restage" {
				first.OperationID = uuid.NewString()
				result, err = s.StageLiteratureCandidate(ctx, "owner", first)
			} else {
				result, err = s.DecideLiteratureCandidate(ctx, "owner", a.Candidate.ID, "accept", LiteratureCandidateAction{OperationID: uuid.NewString()})
			}
			if mode == "conflicting-identifier" || mode == "ambiguous" {
				if !errors.Is(err, ErrLiteratureConflict) {
					t.Fatalf("conflicting alias must not create or select an item: %#v %v", result, err)
				}
				return
			}
			if err != nil || result.Candidate.State != "accepted" {
				t.Fatalf("accept or reconcile: %#v %v", result, err)
			}
			if mode == "foreign-owner" {
				if result.Candidate.ItemID == "foreign" || result.Candidate.ItemID == "" {
					t.Fatalf("foreign alias affected acceptance: %#v", result)
				}
			} else if result.Candidate.ItemID != "existing-b" {
				t.Fatalf("persisted alias was ignored and a duplicate could be created: %#v", result)
			}
		})
	}
}

func TestLiteratureCandidateStageValidatesFinalProviderMetadata(t *testing.T) {
	s, ctx := candidateTestStore(t), context.Background()
	in := candidateInput()
	in.ExternalID = strings.Repeat("a", 40)
	in.Metadata = map[string]any{"abstract": strings.Repeat("a", 16369)}
	raw, err := literatureMetadata(in.Metadata)
	if err != nil || len(raw) != 16384 {
		t.Fatalf("boundary fixture: %d %v", len(raw), err)
	}
	result, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if !errors.Is(err, ErrLiteratureInvalid) {
		_, acceptErr := s.DecideLiteratureCandidate(ctx, "owner", result.Candidate.ID, "accept", LiteratureCandidateAction{OperationID: uuid.NewString()})
		t.Fatalf("stage admitted metadata that cannot be accepted: stage=%v accept=%v", err, acceptErr)
	}
	page, err := s.ListLiteratureCandidates(ctx, "owner", "pending", "")
	if err != nil || len(page.Candidates) != 0 {
		t.Fatalf("rejected admission left an Inbox row: %#v %v", page, err)
	}
}

func TestLiteratureCandidateProviderMetadataBoundaryAndInputReplay(t *testing.T) {
	s, ctx := candidateTestStore(t), context.Background()
	in := candidateInput()
	in.ExternalID = strings.Repeat("a", 40)
	finalEmpty, _ := json.Marshal(map[string]any{"abstract": "", "semantic_scholar_id": in.ExternalID})
	in.Metadata = map[string]any{"abstract": strings.Repeat("a", 16384-len(finalEmpty))}
	first, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := in.Metadata["semantic_scholar_id"]; present {
		t.Fatal("stage changed the caller's input and replay hash")
	}
	action := LiteratureCandidateAction{OperationID: uuid.NewString()}
	accepted, err := s.DecideLiteratureCandidate(ctx, "owner", first.Candidate.ID, "accept", action)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.GetLiteratureItem(ctx, "owner", accepted.Candidate.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := literatureMetadata(item.Metadata)
	if err != nil || len(metadata) != 16384 || item.Metadata["semantic_scholar_id"] != in.ExternalID {
		t.Fatalf("final metadata boundary: %d %#v %v", len(metadata), item.Metadata, err)
	}
	replayed, err := s.StageLiteratureCandidate(ctx, "owner", in)
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("original input must replay the frozen pre-accept receipt: %#v %v", replayed, err)
	}
}
