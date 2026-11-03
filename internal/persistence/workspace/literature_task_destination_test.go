package workspace

import (
	"github.com/google/uuid"
	"reflect"
	"strings"
	"testing"
)

func TestLiteratureTaskAcceptanceLinksOnlyOriginalAvailableProject(t *testing.T) {
	for _, mode := range []string{"available", "deleted", "reuse_rejected"} {
		t.Run(mode, func(t *testing.T) {
			s, _, a := literatureTaskFixture(t)
			request := literatureTaskRequest()
			staged, err := s.SaveTaskLiteratureToInbox(t.Context(), a, request)
			if err != nil {
				t.Fatal(err)
			}
			id := staged.Candidates[0].ID
			if mode != "available" {
				if err = s.DeleteProject(a.Access.Frame.ProjectID); err != nil {
					t.Fatal(err)
				}
				if mode == "reuse_rejected" {
					if _, err = s.CreateProject(CreateProjectInput{ID: a.Access.Frame.ProjectID, UserID: a.Access.UserID, Name: "Replacement destination"}); err == nil || !strings.Contains(err.Error(), "project identity has already been used") {
						t.Fatalf("original project identity guard did not reject reuse: %v", err)
					}
				}
			}
			pending, err := s.ListLiteratureCandidates(t.Context(), a.Access.UserID, "pending", "")
			if err != nil || len(pending.Candidates) != 1 {
				t.Fatalf("pending=%#v err=%v", pending, err)
			}
			origin := pending.Candidates[0].TaskOrigins[0]
			if origin.ProjectAvailable == nil || *origin.ProjectAvailable != (mode == "available") {
				t.Fatalf("availability=%#v", origin)
			}
			action := LiteratureCandidateAction{OperationID: uuid.NewString()}
			accepted, err := s.DecideLiteratureCandidate(t.Context(), a.Access.UserID, id, "accept", action)
			if err != nil || accepted.Candidate.State != "accepted" {
				t.Fatalf("accepted=%#v %v", accepted, err)
			}
			item, err := s.GetLiteratureItem(t.Context(), a.Access.UserID, accepted.Candidate.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			if mode == "available" {
				want = []string{a.Access.Frame.ProjectID}
			}
			if !reflect.DeepEqual(item.ProjectIDs, want) {
				t.Fatalf("projects=%#v want=%#v", item.ProjectIDs, want)
			}
			if mode == "available" {
				if _, err = s.SetLiteratureProject(t.Context(), a.Access.UserID, item.ID, a.Access.Frame.ProjectID, uuid.NewString(), false); err != nil {
					t.Fatal(err)
				}
				if _, err = s.DecideLiteratureCandidate(t.Context(), a.Access.UserID, id, "accept", action); err != nil {
					t.Fatal(err)
				}
				if _, err = s.SaveTaskLiteratureToInbox(t.Context(), a, request); err != nil {
					t.Fatal(err)
				}
				item, err = s.GetLiteratureItem(t.Context(), a.Access.UserID, item.ID)
				if err != nil || len(item.ProjectIDs) != 0 {
					t.Fatalf("old replay recreated membership: %#v %v", item, err)
				}
			}
		})
	}
}

func TestLiteratureTaskExistingItemReuseAndDismissalAreTruthful(t *testing.T) {
	s, _, a := literatureTaskFixture(t)
	request := literatureTaskRequest()
	original, err := s.CreateManualLiterature(t.Context(), a.Access.UserID, LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Original title", Metadata: map[string]any{"doi": "10.1234/task-inbox"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.SaveTaskLiteratureToInbox(t.Context(), a, request)
	if err != nil || result.Candidates[0].State != "accepted" || result.Candidates[0].ItemID != original.Item.ID {
		t.Fatalf("reuse=%#v %v", result, err)
	}
	item, err := s.GetLiteratureItem(t.Context(), a.Access.UserID, original.Item.ID)
	if err != nil || item.Title != "Original title" || !reflect.DeepEqual(item.ProjectIDs, []string{a.Access.Frame.ProjectID}) {
		t.Fatalf("item=%#v %v", item, err)
	}
	pending := literatureTaskRequest()
	pending.References[0].ExternalID = "other"
	pending.References[0].Metadata = map[string]any{}
	staged, err := s.SaveTaskLiteratureToInbox(t.Context(), a, pending)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideLiteratureCandidate(t.Context(), a.Access.UserID, staged.Candidates[0].ID, "dismiss", LiteratureCandidateAction{OperationID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	pending.OperationID = uuid.NewString()
	repeat, err := s.SaveTaskLiteratureToInbox(t.Context(), a, pending)
	if err != nil || repeat.Candidates[0].State != "dismissed" || repeat.Candidates[0].ItemID != "" {
		t.Fatalf("dismissed resurrected: %#v %v", repeat, err)
	}
}
