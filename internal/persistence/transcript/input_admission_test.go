package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestInputAdmissionTracksAdmissionInsteadOfPublication(t *testing.T) {
	for _, stagedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("staged_first_%t", stagedFirst), func(t *testing.T) {
			repo, db, _ := newTranscriptRepository(t)
			ctx := t.Context()
			stream, err := repo.CreateStream(ctx, CreateStreamInput{UID: "admission", OwnerID: "owner", ExternalID: "admission", Kind: StreamKindStandalone, Epoch: 1})
			if err != nil {
				t.Fatal(err)
			}
			input := func(id string) AppendUserEventInput {
				return AppendUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: id, PayloadJSON: []byte(`{"text":"` + id + `"}`)}
			}
			stagedID, appendedID := "B", "A"
			if stagedFirst {
				stagedID, appendedID = "A", "B"
			}
			var staged Event
			if stagedFirst {
				result, err := repo.StageUserEvent(ctx, input(stagedID))
				if err != nil {
					t.Fatal(err)
				}
				staged = result.Event
			}
			appended, created, err := repo.AppendUserEvent(ctx, input(appendedID))
			if err != nil || !created {
				t.Fatalf("append created=%t err=%v", created, err)
			}
			if !stagedFirst {
				result, err := repo.StageUserEvent(ctx, input(stagedID))
				if err != nil {
					t.Fatal(err)
				}
				staged = result.Event
			}
			claim, err := repo.ClaimRunner(ctx, ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
			if err != nil || !claim.Claimed || claim.Claim.ClaimedInputRevision != 1 {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
			for retry := 0; retry < 2; retry++ {
				event, admitted, err := repo.AdmitUserEvent(ctx, AdmitUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: stagedID})
				if err != nil || admitted != (retry == 0) || event.EventID != staged.EventID {
					t.Fatalf("admit retry=%d admitted=%t event=%+v err=%v", retry, admitted, event, err)
				}
			}
			var first, second, count int64
			if err := db.QueryRow(`SELECT COUNT(*),MIN(CASE WHEN input_revision=1 THEN event_id END),MIN(CASE WHEN input_revision=2 THEN event_id END)
				FROM transcript_input_admissions WHERE stream_uid=?`, stream.UID).Scan(&count, &first, &second); err != nil {
				t.Fatal(err)
			}
			if count != 2 || first != appended.EventID || second != staged.EventID {
				t.Fatalf("admission mapped by publication: count=%d first=%d second=%d appended=%d staged=%d", count, first, second, appended.EventID, staged.EventID)
			}
			coverage, err := repo.GetInputCoverage(ctx, stream.UID, stream.OwnerID, claim.Claim.ClaimedInputRevision)
			if err != nil || !coverage.Complete || !coverage.Pending() || !coverage.HasMessage {
				t.Fatalf("coverage=%+v err=%v", coverage, err)
			}
			rollback := errors.New("controlled rollback")
			err = repo.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
				if _, created, err := repo.appendUserEventConn(ctx, tx.conn, input("rollback"), time.Now()); err != nil || !created {
					return fmt.Errorf("append rollback created=%t: %w", created, err)
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("rollback=%v", err)
			}
			var revision, facts int
			if err := db.QueryRow(`SELECT input_revision,(SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=?) FROM transcript_streams WHERE stream_uid=?`, stream.UID, stream.UID).Scan(&revision, &facts); err != nil || revision != 2 || facts != 2 {
				t.Fatalf("rollback revision=%d facts=%d err=%v", revision, facts, err)
			}
		})
	}
}

func admissionFrameFixture(t *testing.T) (*Repository, *sql.DB, Stream, ClaimRunnerResult) {
	t.Helper()
	repo, db, _ := newTranscriptRepository(t)
	if _, err := db.Exec(`INSERT INTO projects(id,user_id) VALUES('p','owner'); INSERT INTO frames(id,project_id,root_frame_id,status) VALUES('f','p','f','processing')`); err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "frame:f", OwnerID: "owner", ExternalID: "f", SessionID: "f", Kind: StreamKindFrameRef, FrameID: "f", RootFrameID: "f", ProjectID: "p", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial", FrameEventID: "initial-frame", MessageUUID: "initial-uuid", Text: "Initial"}); err != nil {
		t.Fatal(err)
	}
	claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	return repo, db, stream, claim
}

func TestInputCoverageKeepsOrdinaryInputBehindLaterControl(t *testing.T) {
	repo, _, stream, claim := admissionFrameFixture(t)
	if _, _, created, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "message", FrameEventID: "message-frame", MessageUUID: "message-uuid", Text: "Additional evidence", MessageOrigin: "input_response"}); err != nil || !created {
		t.Fatalf("message created=%t err=%v", created, err)
	}
	err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), AppendFrameInputResponseInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, FrameID: stream.FrameID,
			ClientMessageID: "control", PayloadJSON: []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"question","content":"yes"}]}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := repo.GetInputCoverage(t.Context(), stream.UID, stream.OwnerID, claim.Claim.ClaimedInputRevision)
	if err != nil || !coverage.Complete || !coverage.HasMessage || !coverage.HasControl || coverage.InputRevision != 3 {
		t.Fatalf("coverage=%+v err=%v", coverage, err)
	}
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "finish", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	next, err := repo.ClaimNextFrameRunner(t.Context(), ClaimNextFrameRunnerInput{RunnerID: "next", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision != 3 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	if duplicate, err := repo.ClaimNextFrameRunner(t.Context(), ClaimNextFrameRunnerInput{RunnerID: "duplicate", TTL: time.Minute}); err != nil || duplicate.Claimed {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
}

func TestInputAdmissionRejectsSourceAndKindMutation(t *testing.T) {
	repo, db, stream, claim := admissionFrameFixture(t)
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "late", FrameEventID: "late-frame", MessageUUID: "late-uuid", Text: "Later", MessageOrigin: "input_response"}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE transcript_input_admissions SET input_kind='control' WHERE input_revision=2`,
		`DELETE FROM transcript_input_admissions WHERE input_revision=2`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("ledger changed: %s", statement)
		}
	}
	if _, err := repo.GetInputCoverage(t.Context(), stream.UID, "other", claim.Claim.ClaimedInputRevision); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("owner error=%v", err)
	}
	// A broken fact is not a legacy gap. Controlled corruption bypasses only
	// this test's immutability guard, then exercises the production reader.
	if _, err := db.Exec(`DROP TRIGGER transcript_input_admissions_immutable; UPDATE transcript_input_admissions SET input_kind='control' WHERE input_revision=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInputCoverage(context.Background(), stream.UID, stream.OwnerID, claim.Claim.ClaimedInputRevision); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("corrupt source classification accepted: %v", err)
	}
}
