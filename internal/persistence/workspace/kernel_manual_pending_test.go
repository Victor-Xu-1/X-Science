package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newManualKernelPendingStoreFixture(t *testing.T) (*Store, KernelFrameAccess, AdmitManualKernelExecutionInput) {
	t.Helper()
	store, previous, input := newManualKernelStoreFixture(t)
	if _, err := store.CreateFrame(CreateFrameInput{ID: "pending-window", ProjectID: previous.Frame.ProjectID,
		AgentName: "agent", Status: "completed", ConversationType: "task"}); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccess("pending-window")
	if err != nil || !found {
		t.Fatalf("pending scope found=%t err=%v", found, err)
	}
	input.Binding = manualKernelAccessBinding(access)
	input.Binding.ExecutionID = ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, input.Request.RequestID)
	input.Binding.KernelID, input.Binding.KernelGeneration, input.Binding.KernelKind, input.Binding.ServerBootID = "original-kernel", 7, "analysis", "old-boot"
	return store, access, input
}

func addManualKernelPendingOrdinaryEvents(t *testing.T, store *Store, frameID string, count int) {
	t.Helper()
	if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
		var sequence int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM frame_events WHERE frame_id=?`, frameID).Scan(&sequence); err != nil {
			return err
		}
		for index := 0; index < count; index++ {
			_, err := tx.Exec(`INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at) VALUES(?,?,?,?,?,CURRENT_TIMESTAMP)`,
				uuid.NewString(), frameID, sequence+int64(index)+1, "frame_activity", "not JSON: ordinary payload must not be decoded")
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManualKernelPendingWindowBoundaryAndStrictCanonicalPayload(t *testing.T) {
	for _, ordinary := range []int{127, 128} {
		t.Run(fmt.Sprint(ordinary), func(t *testing.T) {
			store, access, input := newManualKernelPendingStoreFixture(t)
			record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			addManualKernelPendingOrdinaryEvents(t, store, access.Frame.ID, ordinary)
			page, err := store.ListManualKernelPending(context.Background(), access, "")
			if err != nil {
				t.Fatal(err)
			}
			if ordinary == 127 {
				if len(page.Records) != 1 || !page.ScanComplete || page.NextCursor != "" {
					t.Fatalf("exact 128-event window=%#v", page)
				}
			} else {
				if len(page.Records) != 0 || page.ScanComplete || page.NextCursor == "" {
					t.Fatalf("129-event window=%#v", page)
				}
				page, err = store.ListManualKernelPending(context.Background(), access, page.NextCursor)
				if err != nil || len(page.Records) != 1 || !page.ScanComplete {
					t.Fatalf("lookahead was skipped: %#v err=%v", page, err)
				}
			}
			if page.Records[0].ExecutionID != record.ExecutionID {
				t.Fatal("window changed receipt identity")
			}
			// Corrupt only the selected canonical manual payload. Ordinary
			// non-JSON history above must never have entered this decoder.
			if _, err := store.db.Exec(`UPDATE frame_events SET payload=json_set(payload,'$.unexpected',true) WHERE id=?`, manualKernelEventID(record.ExecutionID)); err != nil {
				t.Fatal(err)
			}
			cursor, err := encodeManualKernelPendingCursor(newManualKernelPendingCursor(input.Binding, int64(ordinary+1), 2))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ListManualKernelPending(context.Background(), access, cursor); !errors.Is(err, ErrManualKernelExecutionInvalid) {
				t.Fatalf("invalid canonical payload accepted: %v", err)
			}
		})
	}
}

func TestManualKernelPendingPreservesAllNonterminalFactsAndExactOwnerScope(t *testing.T) {
	store, access, input := newManualKernelPendingStoreFixture(t)
	want := map[string]ManualKernelExecution{}
	for _, state := range []string{"queued", "started", "cancel_requested", "outcome_unknown"} {
		input.Request.RequestID = uuid.NewString()
		input.Binding.ExecutionID = ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, input.Request.RequestID)
		record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		switch state {
		case "started":
			record, err = store.MarkManualKernelExecutionStarted(context.Background(), record, time.Now().UTC())
		case "cancel_requested":
			record, err = store.RequestManualKernelExecutionCancellation(context.Background(), record)
		case "outcome_unknown":
			record, err = store.MarkManualKernelExecutionUnknown(context.Background(), record, "unconfirmed_terminal")
		}
		if err != nil {
			t.Fatal(err)
		}
		want[record.ExecutionID] = record
	}
	page, err := store.ListManualKernelPending(context.Background(), access, "")
	if err != nil || !page.ScanComplete || len(page.Records) != 4 {
		t.Fatalf("pending facts=%#v err=%v", page, err)
	}
	for _, record := range page.Records {
		expected := want[record.ExecutionID]
		if record.State != expected.State || record.StateVersion != expected.StateVersion || record.RequestSHA256 != expected.RequestSHA256 || record.ServerBootID != "old-boot" {
			t.Fatalf("readonly discovery altered fact=%#v", record)
		}
	}
	stale := access
	stale.Frame.IncarnationID = "new-frame-incarnation"
	if _, err := store.ListManualKernelPending(context.Background(), stale, ""); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("stale scope=%v", err)
	}
	if _, err := store.db.Exec(`UPDATE projects SET user_id='replacement-owner' WHERE id=?`, access.Frame.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListManualKernelPending(context.Background(), access, ""); err == nil {
		t.Fatal("former owner discovered receipts")
	}
	access.UserID = "replacement-owner"
	page, err = store.ListManualKernelPending(context.Background(), access, "")
	if err != nil || len(page.Records) != 0 {
		t.Fatalf("new owner inherited old bound receipts=%#v err=%v", page, err)
	}
}

func TestManualKernelPendingOutputBoundaryKeepsLastActuallyScannedPosition(t *testing.T) {
	for _, count := range []int{32, 33} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store, access, input := newManualKernelPendingStoreFixture(t)
			for index := 0; index < count; index++ {
				input.Request.RequestID = uuid.NewString()
				input.Binding.ExecutionID = ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, input.Request.RequestID)
				if _, _, err := store.AdmitManualKernelExecution(context.Background(), input); err != nil {
					t.Fatal(err)
				}
			}
			page, err := store.ListManualKernelPending(context.Background(), access, "")
			if err != nil || len(page.Records) != 32 || page.ScanComplete != (count == 32) {
				t.Fatalf("output boundary=%#v err=%v", page, err)
			}
			if count == 32 {
				if page.NextCursor != "" {
					t.Fatal("exact output boundary invented more history")
				}
				return
			}
			position, err := decodeManualKernelPendingCursor(page.NextCursor, manualKernelAccessBinding(access))
			if err != nil || position.Before != 2 {
				t.Fatalf("cursor skipped unscanned receipt=%#v err=%v", position, err)
			}
			last, err := store.ListManualKernelPending(context.Background(), access, page.NextCursor)
			if err != nil || len(last.Records) != 1 || !last.ScanComplete || last.ThroughSequence != page.ThroughSequence {
				t.Fatalf("boundary continuation=%#v err=%v", last, err)
			}
		})
	}
}
