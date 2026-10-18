package server

import (
	"fmt"
	"testing"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
)

func TestNotificationInboxPublicOrdersRFC3339NanoByInstant(t *testing.T) {
	state, err := decodeNotificationInboxState(runtimekv.Entry{}, false, "timestamp-owner")
	if err != nil {
		t.Fatal(err)
	}
	state.Revision = 1
	whole := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	fraction := whole.Add(100 * time.Millisecond)
	older := notificationInboxTimestampRecord("whole-second", whole)
	newer := notificationInboxTimestampRecord("fractional-second", fraction)
	state.Items = []notificationInboxRecord{older, newer}
	if older.CreatedAt != "2026-10-18T12:00:00Z" || newer.CreatedAt != "2026-10-18T12:00:00.1Z" {
		t.Fatal("fixture must exercise different valid RFC3339Nano precision in the same second")
	}
	// This calls the real public snapshot constructor, not a test-local sorter.
	snapshot := notificationInboxPublic(state)
	if len(snapshot.Items) != 2 || snapshot.Items[0].ID != newer.ID || snapshot.Items[1].ID != older.ID {
		t.Fatalf("public snapshot sorted timestamps lexically instead of by instant: %#v", snapshot.Items)
	}
}

func TestNotificationInboxRetentionUsesRFC3339NanoInstantAtHistoryBoundary(t *testing.T) {
	state, err := decodeNotificationInboxState(runtimekv.Entry{}, false, "timestamp-owner")
	if err != nil {
		t.Fatal(err)
	}
	state.Revision = 1
	whole := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	older := notificationInboxTimestampRecord("boundary-whole-second", whole)
	newer := notificationInboxTimestampRecord("boundary-fractional-second", whole.Add(100*time.Millisecond))
	state.Items = []notificationInboxRecord{older, newer}
	// Exactly 999 definitely newer history records leave only one slot for
	// the same-second pair. The fractional-second record must occupy it.
	for index := 1; index <= 999; index++ {
		state.Items = append(state.Items, notificationInboxTimestampRecord(
			fmt.Sprintf("newer-history-%04d", index), whole.Add(time.Duration(index)*time.Second),
		))
	}
	if err := retainNotificationInbox(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 1000 || !state.HistoryTruncated {
		t.Fatalf("history boundary not exercised: count=%d truncated=%t", len(state.Items), state.HistoryTruncated)
	}
	keptOlder, keptNewer := false, false
	for _, item := range state.Items {
		keptOlder = keptOlder || item.ID == older.ID
		keptNewer = keptNewer || item.ID == newer.ID
	}
	if keptOlder || !keptNewer {
		t.Fatalf("history retention kept the earlier instant: older=%t newer=%t", keptOlder, keptNewer)
	}
}

func notificationInboxTimestampRecord(origin string, created time.Time) notificationInboxRecord {
	return notificationInboxRecord{
		notificationInboxItem: notificationInboxItem{
			ID: notificationInboxID(origin), OriginID: origin, Kind: "task.completed",
			ConversationID: "timestamp-frame", FrameIncarnationID: "timestamp-incarnation",
			Title: "Timestamp ordering", CreatedAt: created.Format(time.RFC3339Nano),
		},
		ObservedRevision: 1,
	}
}
