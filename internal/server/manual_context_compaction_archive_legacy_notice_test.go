package server

import (
	"context"
	"strconv"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionArchiveNoticeRetainsExistingArchiveIdentity(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	if err := f.server.persistCanonicalContextCompactionArchive(context.Background(), f.stream.OwnerID, receipt); err != nil {
		t.Fatal(err)
	}
	archive, found, err := f.store.GetCompactionArchive(f.stream.FrameID, 0)
	if err != nil || !found {
		t.Fatalf("canonical fixture archive found=%t err=%v", found, err)
	}
	if _, err := f.db.Exec(`DELETE FROM compaction_archives WHERE id=?`, archive.ID); err != nil {
		t.Fatal(err)
	}
	const existingID = "9edec9c5-43a1-41e2-a39f-fb63f4e3df03"
	adopted, err := f.store.CreateCompactionArchive(workspace.CreateCompactionArchiveInput{Context: context.Background(),
		CanonicalSource: &workspace.ContextCompactionArchiveSource{OwnerID: f.stream.OwnerID, Request: receipt.Request},
		ID:              existingID, FrameID: archive.FrameID, MessageCount: archive.MessageCount, TokenCount: archive.TokenCount,
		Summary: archive.Summary, Messages: archive.Messages, SourceEventID: archive.SourceEventID, CreatedAt: archive.CreatedAt})
	if err != nil || adopted.ID != existingID {
		t.Fatalf("existing valid canonical archive was not created with its own identity: %#v %v", adopted, err)
	}
	f.server.repairContextCompactionArchivePass(context.Background(), time.Now().UTC())
	id := "context-compaction-archive:" + receipt.Request.RequestID + ":" + strconv.FormatInt(receipt.BoundaryEventID, 10)
	if _, found, err := f.store.GetRealtimeEventByID(id); err != nil || !found {
		t.Fatalf("existing archive did not get its canonical ready notice: found=%t err=%v", found, err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := f.db.Exec(`UPDATE realtime_events SET created_at=? WHERE id=?; UPDATE frames SET status='completed' WHERE id=?`, old, id, f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SweepDataLifecycle(context.Background(), workspace.DefaultDataLifecyclePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.GetRealtimeEventByID(id); err != nil || !found {
		t.Fatalf("valid legacy-ID archive-ready notice was incorrectly reclaimed: found=%t err=%v", found, err)
	}
	current, found, err := f.store.GetCompactionArchive(f.stream.FrameID, 0)
	if err != nil || !found || current.ID != existingID {
		t.Fatalf("derived recovery changed existing archive identity: %#v found=%t err=%v", current, found, err)
	}
}
