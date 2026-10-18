package server

import (
	"net/http"
	"testing"
)

func TestNotificationInboxCanonicalCompletionSurvivesTransportCleanupAndRestart(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "completed-one")
	first := readNotificationInbox(t, f, "inbox-owner")
	if len(first.items) != 1 || first.unread != 1 || first.items[0]["kind"] != "task.completed" || first.items[0]["conversation_id"] != "completed-one" {
		t.Fatalf("canonical outcome missing=%#v", first)
	}
	id := first.items[0]["id"]
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read", map[string]any{"ids": []any{id}}, "inbox-owner", http.StatusOK)
	// Realtime is a short-lived accelerator, not the inbox history authority.
	if _, err := f.db.Exec(`DELETE FROM realtime_events WHERE user_id=?`, "inbox-owner"); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	second := readNotificationInbox(t, f, "inbox-owner")
	if len(second.items) != 1 || second.unread != 0 || second.items[0]["id"] != id || second.items[0]["read_at"] == nil {
		t.Fatalf("restart lost durable human read state=%#v", second)
	}
	third := readNotificationInbox(t, f, "inbox-owner")
	if len(third.items) != 1 || third.items[0]["id"] != id || third.unread != 0 {
		t.Fatalf("canonical reconciliation duplicated or revived read item=%#v", third)
	}
}

func TestNotificationInboxOldReadAllDoesNotReadNewCanonicalEvent(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "before-read-all")
	before := readNotificationInbox(t, f, "inbox-owner")
	f.complete(t, "inbox-owner", "after-read-all")
	after := readNotificationInbox(t, f, "inbox-owner")
	if len(after.items) != 2 || after.unread != 2 {
		t.Fatalf("second canonical outcome missing=%#v", after)
	}
	input := map[string]any{"read_receipt": before.receipt}
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all", input, "inbox-owner", http.StatusOK)
	current := readNotificationInbox(t, f, "inbox-owner")
	if current.unread != 1 {
		t.Fatalf("old read-all swallowed newly arrived event=%#v", current)
	}
	for _, item := range current.items {
		if item["conversation_id"] == "after-read-all" && item["read_at"] != nil {
			t.Fatal("new event was marked read by an older snapshot")
		}
		if item["conversation_id"] == "before-read-all" && item["read_at"] == nil {
			t.Fatal("observed old event was not marked read")
		}
	}
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all", input, "inbox-owner", http.StatusOK)
	if replay := readNotificationInbox(t, f, "inbox-owner"); replay.unread != 1 {
		t.Fatal("read-all replay changed a newer item")
	}
}

func TestNotificationInboxOwnerReceiptAndTargetIncarnationAreIsolated(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "owner-a", "owner-a-frame")
	f.complete(t, "owner-b", "owner-b-frame")
	a := readNotificationInbox(t, f, "owner-a")
	b := readNotificationInbox(t, f, "owner-b")
	if len(a.items) != 1 || len(b.items) != 1 || a.items[0]["conversation_id"] != "owner-a-frame" || b.items[0]["conversation_id"] != "owner-b-frame" {
		t.Fatalf("cross-owner inbox a=%#v b=%#v", a, b)
	}
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all", map[string]any{"read_receipt": a.receipt}, "owner-b", http.StatusConflict)
	if after := readNotificationInbox(t, f, "owner-a"); after.unread != 1 {
		t.Fatal("foreign read-all altered the original owner")
	}
	if _, err := f.db.Exec(`UPDATE frames SET incarnation_id='replacement-incarnation' WHERE id='owner-a-frame'`); err != nil {
		t.Fatal(err)
	}
	invalid := readNotificationInbox(t, f, "owner-a")
	if len(invalid.items) != 1 || invalid.items[0]["id"] != a.items[0]["id"] || invalid.items[0]["target_invalidated_at"] == nil {
		t.Fatalf("stale notification rebound to replacement frame=%#v", invalid)
	}
}

func TestNotificationInboxRecoversCanonicalCompletionWithoutRealtimeRows(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "pre-inbox-completion")
	// No inbox read has occurred. An independent bounded reconciliation must
	// recover the actual receipt after transport cleanup, not invent a finish.
	if _, err := f.db.Exec(`DELETE FROM realtime_events WHERE user_id=?`, "inbox-owner"); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	snapshot := readNotificationInbox(t, f, "inbox-owner")
	if len(snapshot.items) != 1 || snapshot.items[0]["conversation_id"] != "pre-inbox-completion" || snapshot.items[0]["kind"] != "task.completed" {
		t.Fatalf("reconciliation depended on transient realtime rows=%#v", snapshot)
	}
}
