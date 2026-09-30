package transcript

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func assertInputWake(t *testing.T, wake <-chan struct{}, want bool) {
	t.Helper()
	select {
	case <-wake:
		if !want {
			t.Fatal("input wake published without a new committed input")
		}
	default:
		if want {
			t.Fatal("new committed input did not publish its wake")
		}
	}
}

func TestInputWakeFrameCommitRollbackReplayAndOwnerBoundary(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	if _, err := db.Exec(`INSERT INTO projects(id,user_id) VALUES('project','owner');
		INSERT INTO frames(id,project_id,root_frame_id) VALUES('frame','project','frame')`); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := repo.CreateStream(ctx, CreateStreamInput{
		UID: "frame:frame", OwnerID: "owner", ExternalID: "frame", SessionID: "frame",
		Kind: StreamKindFrameRef, ProjectID: "project", RootFrameID: "frame", FrameID: "frame", Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	input := AppendFrameUserEventInput{
		StreamUID: "frame:frame", OwnerID: "owner", ClientMessageID: "input-1",
		FrameEventID: "event-1", MessageUUID: "message-1", Text: "Run the task.", Destinations: []string{"ws"},
	}
	wake := repo.InputWake()
	wantRollback := errors.New("rollback fixture")
	err := repo.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
		if _, _, created, err := tx.AppendFrameUserEvent(ctx, input); err != nil || !created {
			t.Fatalf("append in transaction created=%t err=%v", created, err)
		}
		assertInputWake(t, wake, false)
		return wantRollback
	})
	if !errors.Is(err, wantRollback) {
		t.Fatalf("rollback: %v", err)
	}
	assertInputWake(t, wake, false)
	stream, err := repo.GetStream(ctx, input.StreamUID, input.OwnerID)
	if err != nil || stream.InputRevision != 0 {
		t.Fatalf("rolled-back input remained runnable: %#v %v", stream, err)
	}
	outcome, err := repo.RunImmediateWithOutcome(ctx, func(tx *ImmediateTransaction) error {
		if _, _, _, err := tx.AppendFrameUserEvent(ctx, input); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO transcript_delivery_routes
			(stream_uid,destination,current_generation,status,updated_at)
			VALUES('missing-stream','ws',1,'active',CURRENT_TIMESTAMP)`)
		return err
	})
	if err == nil || !outcome.CommitAttempted || outcome.Committed {
		t.Fatalf("failed COMMIT outcome=%#v err=%v", outcome, err)
	}
	assertInputWake(t, wake, false)
	if err := repo.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
		_, _, _, err := tx.AppendFrameUserEvent(ctx, input)
		assertInputWake(t, wake, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertInputWake(t, wake, true)
	next := repo.InputWake()
	if next == wake {
		t.Fatal("wake generation was not replaced")
	}
	if _, _, created, err := repo.AppendFrameUserEvent(ctx, input); err != nil || created {
		t.Fatalf("idempotent replay: created=%t err=%v", created, err)
	}
	assertInputWake(t, next, false)
	foreign := input
	foreign.OwnerID = "foreign"
	if _, _, _, err := repo.AppendFrameUserEvent(ctx, foreign); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("owner boundary: %v", err)
	}
	assertInputWake(t, next, false)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := repo.AppendFrameUserEvent(cancelled, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled append: %v", err)
	}
	assertInputWake(t, next, false)
}

func TestInputWakeStagedInputWaitsForAdmission(t *testing.T) {
	repo, _, _ := newTranscriptRepository(t)
	ctx := t.Context()
	if _, err := repo.CreateStream(ctx, CreateStreamInput{
		UID: "staged", OwnerID: "owner", ExternalID: "external", Kind: StreamKindStandalone, Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	wake := repo.InputWake()
	if _, err := repo.StageUserEvent(ctx, AppendUserEventInput{
		StreamUID: "staged", OwnerID: "owner", ClientMessageID: "input", PayloadJSON: []byte(`{"text":"work"}`),
	}); err != nil {
		t.Fatal(err)
	}
	assertInputWake(t, wake, false)
	input := AdmitUserEventInput{StreamUID: "staged", OwnerID: "owner", ClientMessageID: "input"}
	if _, admitted, err := repo.AdmitUserEvent(ctx, input); err != nil || !admitted {
		t.Fatalf("admit: admitted=%t err=%v", admitted, err)
	}
	assertInputWake(t, wake, true)
	next := repo.InputWake()
	if _, admitted, err := repo.AdmitUserEvent(ctx, input); err != nil || admitted {
		t.Fatalf("repeated admit: admitted=%t err=%v", admitted, err)
	}
	assertInputWake(t, next, false)
}

func TestInputWakeConcurrentDuplicateInputCommitsOneRevision(t *testing.T) {
	repo, _, _ := newTranscriptRepository(t)
	ctx := t.Context()
	if _, err := repo.CreateStream(ctx, CreateStreamInput{
		UID: "concurrent", OwnerID: "owner", ExternalID: "external", Kind: StreamKindStandalone, Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	wake := repo.InputWake()
	var group sync.WaitGroup
	var createdCount atomic.Int32
	for range 8 {
		group.Go(func() {
			_, created, err := repo.AppendUserEvent(ctx, AppendUserEventInput{
				StreamUID: "concurrent", OwnerID: "owner", ClientMessageID: "same-input", PayloadJSON: []byte(`{"text":"work"}`),
			})
			if err != nil {
				t.Errorf("concurrent append: %v", err)
			}
			if created {
				createdCount.Add(1)
			}
		})
	}
	group.Wait()
	stream, err := repo.GetStream(ctx, "concurrent", "owner")
	if err != nil || createdCount.Load() != 1 || stream.InputRevision != 1 {
		t.Fatalf("duplicate input commits: created=%d revision=%d err=%v", createdCount.Load(), stream.InputRevision, err)
	}
	assertInputWake(t, wake, true)
	assertInputWake(t, repo.InputWake(), false)
}
