package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"testing"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestLiteratureTaskInboxAtomicAdmissionAndRollback(t *testing.T) {
	for _, mode := range []string{"second_reference_invalid", "too_many", "storage_second_write", "cancel_after_staging"} {
		t.Run(mode, func(t *testing.T) {
			s, _, a := literatureTaskFixture(t)
			request := literatureTaskRequest()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			second := LiteratureTaskReference{Provider: "crossref", ExternalID: "10.1234/second", Title: "Second", Metadata: map[string]any{"doi": "10.1234/second"}}
			switch mode {
			case "second_reference_invalid":
				second.Title = ""
				request.References = append(request.References, second)
			case "too_many":
				for len(request.References) < 11 {
					request.References = append(request.References, second)
				}
			case "storage_second_write":
				request.References = append(request.References, second)
				if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_library_second BEFORE INSERT ON literature_candidates WHEN NEW.external_id='10.1234/second' BEGIN SELECT RAISE(ABORT,'injected storage failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "cancel_after_staging":
				a.KernelGeneration = 1
				checks := 0
				a.CurrentKernelGeneration = func() uint64 {
					checks++
					if checks == 2 {
						cancel()
					}
					return 1
				}
			}
			receipt, err := s.SaveTaskLiteratureToInbox(ctx, a, request)
			if err == nil || len(receipt.Candidates) != 0 {
				t.Fatalf("batch partially accepted: %#v %v", receipt, err)
			}
			if (mode == "second_reference_invalid" || mode == "too_many") && !errors.Is(err, ErrLiteratureInvalid) {
				t.Fatalf("wrong invalid code: %v", err)
			}
			literatureTaskCounts(t, s, 0)
		})
	}
}

func TestLiteratureTaskInboxExactReceiptSurvivesLostReplyAndStaleClaim(t *testing.T) {
	s, repo, a := literatureTaskFixture(t)
	request := literatureTaskRequest()
	first, err := s.SaveTaskLiteratureToInbox(t.Context(), a, request)
	if err != nil || len(first.Candidates) != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	candidate := first.Candidates[0]
	if candidate.State != "pending" || candidate.ItemID != "" || len(candidate.TaskOrigins) != 1 {
		t.Fatalf("candidate=%#v", candidate)
	}
	origin := candidate.TaskOrigins[0]
	if origin.FrameID != a.Access.Frame.ID || origin.ProjectID != a.Access.Frame.ProjectID || origin.ProjectAvailable == nil || !*origin.ProjectAvailable {
		t.Fatalf("untrusted origin=%#v", origin)
	}
	if candidate.Metadata["precise"] != json.Number("9007199254740993") {
		t.Fatalf("precision=%#v", candidate.Metadata)
	}
	if _, err = repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: a.Claim, ClientMessageID: "library-interruption", ReasonCode: "runtime_draining", Resumable: true}); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var databaseName, databasePath string
	if err = s.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &databaseName, &databasePath); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	replay, err := s.SaveTaskLiteratureToInbox(t.Context(), a, request)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("lost reply changed: %#v %v", replay, err)
	}
	changed := request
	changed.References = append([]LiteratureTaskReference(nil), request.References...)
	changed.References[0].Title = "changed"
	if _, err = s.SaveTaskLiteratureToInbox(t.Context(), a, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed request=%v", err)
	}
	request.OperationID = uuid.NewString()
	if _, err = s.SaveTaskLiteratureToInbox(t.Context(), a, request); !errors.Is(err, transcriptstore.ErrClaimStale) {
		t.Fatalf("stale new operation=%v", err)
	}
	literatureTaskCounts(t, s, 1)
}

func TestLiteratureTaskInboxRejectsForeignAndReplacedAuthority(t *testing.T) {
	for _, mode := range []string{"owner", "project", "frame_incarnation", "root_incarnation", "input_revision", "missing_tool", "kernel_generation"} {
		t.Run(mode, func(t *testing.T) {
			s, _, a := literatureTaskFixture(t)
			switch mode {
			case "owner":
				a.Access.UserID = "foreign"
			case "project":
				a.Access.Frame.ProjectID = "foreign"
			case "frame_incarnation":
				a.Access.Frame.IncarnationID = "replaced"
			case "root_incarnation":
				a.Access.RootFrameIncarnationID = "replaced"
			case "input_revision":
				a.Claim.ClaimedInputRevision++
			case "missing_tool":
				a.ToolCallID = "not-admitted"
			case "kernel_generation":
				a.KernelGeneration = 1
				a.CurrentKernelGeneration = func() uint64 { return 2 }
			}
			if _, err := s.SaveTaskLiteratureToInbox(t.Context(), a, literatureTaskRequest()); err == nil {
				t.Fatal("invalid authority staged metadata")
			}
			literatureTaskCounts(t, s, 0)
		})
	}
}
