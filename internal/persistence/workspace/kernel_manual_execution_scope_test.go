package workspace

import (
	"context"
	"testing"
)

func TestManualKernelRetiredScopeRequiresDeletionOrIncarnationProof(t *testing.T) {
	store, access, input := newManualKernelStoreFixture(t)
	record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertRetired := func(want bool) {
		t.Helper()
		retired, err := store.ManualKernelExecutionScopeRetired(context.Background(), record)
		if err != nil || retired != want {
			t.Fatalf("retired=%t want=%t err=%v", retired, want, err)
		}
	}
	assertRetired(false)
	if _, err := store.db.Exec(`UPDATE frames SET incarnation_id='replacement' WHERE id=?`, record.FrameID); err != nil {
		t.Fatal(err)
	}
	assertRetired(true)
	if _, err := store.db.Exec(`UPDATE frames SET incarnation_id=? WHERE id=?`, record.FrameIncarnationID, record.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM frame_events WHERE id=?`, manualKernelEventID(record.ExecutionID)); err != nil {
		t.Fatal(err)
	}
	assertRetired(false) // A missing receipt does not prove scope retirement.
	if _, err := store.db.Exec(`UPDATE projects SET user_id='different-owner' WHERE id=?`, record.ProjectID); err != nil {
		t.Fatal(err)
	}
	assertRetired(false) // Nor does a permission mismatch.
	if _, err := store.db.Exec(`UPDATE projects SET user_id=? WHERE id=?`, record.OwnerID, record.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteCompatibilityFrameTree(access.Frame.ID, access.UserID, access.Frame.IncarnationID); err != nil {
		t.Fatal(err)
	}
	assertRetired(true)
}
