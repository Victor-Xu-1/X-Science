package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNotificationInboxCapacityPreservesUnresolvedItemsAndSourcePosition(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "a-observed")
	before := readNotificationInbox(t, f, "inbox-owner")
	// Saturate only the derived checkpoint map, not canonical task facts.
	err := f.runtime.EditNamespace(notificationInboxTestNamespace("inbox-owner"), func(entries map[string]runtimekv.Entry) (bool, error) {
		state, err := decodeNotificationInboxState(entries["snapshot"], true, "inbox-owner")
		if err != nil {
			return false, err
		}
		for index := 1; index < 4096; index++ {
			state.Positions[fmt.Sprintf("bounded-checkpoint-%04d", index)] = transcriptstore.NotificationSourcePosition{Incarnation: "fixture-incarnation", Publication: 1}
		}
		state.Revision++
		return true, storeNotificationInbox(entries, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.complete(t, "inbox-owner", "b-unobserved")
	limited := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if limited["reconciliation_incomplete"] != true || limited["incomplete_reason"] != "capacity" || len(limited["items"].([]any)) != 1 {
		t.Fatalf("capacity was silently discarded=%#v", limited)
	}
	entry, found, _ := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, advanced := state.Positions["stream-b-unobserved"]; advanced || state.Items[0].ID != before.items[0]["id"] {
		t.Fatal("capacity advanced or replaced the source")
	}
	// Main Web delivery already settled inside complete(), independently of
	// this full derived store. Resolved/history retention never evicts pending.
	state.Items = nil
	for index := 0; index < 1001; index++ {
		state.Items = append(state.Items, notificationInboxRecord{notificationInboxItem: notificationInboxItem{ID: fmt.Sprint(index), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}})
	}
	state.Items = append(state.Items, notificationInboxRecord{notificationInboxItem: notificationInboxItem{ID: "unresolved", ActionState: "pending", CreatedAt: "2000-01-01T00:00:00Z"}})
	if err := retainNotificationInbox(&state); err != nil || len(state.Items) != 1001 || !state.HistoryTruncated {
		t.Fatalf("retention err=%v count=%d", err, len(state.Items))
	}
	kept := false
	for _, item := range state.Items {
		if item.ID == "unresolved" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("old unresolved pending was pruned with history")
	}
	for index := 0; index < 200; index++ {
		state.Items = append(state.Items, notificationInboxRecord{notificationInboxItem: notificationInboxItem{ActionState: "pending"}})
	}
	if err := retainNotificationInbox(&state); !errors.Is(err, errNotificationInboxCapacity) {
		t.Fatalf("pending overflow not explicit: %v", err)
	}
}

func TestNotificationInboxStrictReadContractAndFullMutationSnapshot(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "read-contract")
	before := readNotificationInbox(t, f, "inbox-owner")
	for _, body := range []any{
		map[string]any{"ids": []string{}},
		map[string]any{"ids": []string{strings.Repeat("x", 129)}},
		map[string]any{"ids": []any{before.items[0]["id"]}, "owner_id": "other"},
	} {
		memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read", body, "inbox-owner", http.StatusBadRequest)
	}
	unknown := map[string]any{"ids": []any{before.items[0]["id"], "foreign-item"}}
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read", unknown, "inbox-owner", http.StatusConflict)
	if after := readNotificationInbox(t, f, "inbox-owner"); after.unread != 1 {
		t.Fatal("partially applied invalid read IDs")
	}
	result := memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read", map[string]any{"ids": []any{before.items[0]["id"]}}, "inbox-owner", http.StatusOK)
	if result["schema"] != notificationInboxSchema || result["unread_count"] != float64(0) || len(result["items"].([]any)) != 1 || result["read_receipt"] == "" {
		t.Fatalf("read result is not complete typed snapshot=%#v", result)
	}
}
