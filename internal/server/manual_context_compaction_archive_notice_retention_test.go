package server

import (
	"context"
	"strconv"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionArchiveNoticeRetentionRequiresCanonicalSource(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	f.server.repairContextCompactionArchivePass(context.Background(), time.Now().UTC())
	id := "context-compaction-archive:" + receipt.Request.RequestID + ":" + strconv.FormatInt(receipt.BoundaryEventID, 10)
	ready, found, err := f.store.GetRealtimeEventByID(id)
	if err != nil || !found {
		t.Fatalf("real canonical ready receipt missing: found=%t err=%v", found, err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := f.db.Exec(`UPDATE realtime_events SET created_at=? WHERE id=?; UPDATE frames SET status='completed' WHERE id=?`, old, id, f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	// A real legacy archive and a matching-looking ready notice are still not
	// a canonical compaction boundary. Prefixes and JSON markers grant nothing.
	const fakeRequest = "d71f7ff0-76a1-42be-88ea-466860ff0ee5"
	const ordinarySource = int64(1)
	if _, err := f.store.CreateCompactionArchive(workspace.CreateCompactionArchiveInput{
		ID: "context-compaction-archive:" + fakeRequest, FrameID: f.stream.FrameID, SourceEventID: func() *int64 { n := ordinarySource; return &n }(),
		Summary: "Unproven legacy archive", Messages: []map[string]any{}, CreatedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	fake := map[string]any{}
	for key, value := range ready.Payload {
		fake[key] = value
	}
	fake["request_id"] = fakeRequest
	fakeID := "context-compaction-archive:" + fakeRequest + ":1"
	if _, err := f.store.AppendRealtimeEvent(workspace.RealtimeEventInput{ID: fakeID, UserID: ready.UserID,
		ProjectID: ready.ProjectID, RootFrameID: ready.RootFrameID, FrameID: ready.FrameID, Type: ready.Type, Payload: fake, OccurredAt: old}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SweepDataLifecycle(context.Background(), workspace.DefaultDataLifecyclePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.GetRealtimeEventByID(id); err != nil || !found {
		t.Fatalf("daily transport sweep erased the proven projection completion: found=%t err=%v", found, err)
	}
	if _, found, err := f.store.GetRealtimeEventByID(fakeID); err != nil || found {
		t.Fatalf("unproven lookalike became permanent: found=%t err=%v", found, err)
	}
	// Once the referenced archive is gone, the same old notice is ordinary
	// reclaimable transport again; retention does not invent an orphan ledger.
	if _, err := f.db.Exec(`DELETE FROM compaction_archives WHERE id=?`, "context-compaction-archive:"+receipt.Request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SweepDataLifecycle(context.Background(), workspace.DefaultDataLifecyclePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.GetRealtimeEventByID(id); err != nil || found {
		t.Fatalf("orphan archive-ready notice was retained: found=%t err=%v", found, err)
	}
}
