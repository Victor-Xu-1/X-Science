package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNotificationInboxReconciliationReservesAllReadMetadata(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "a-growth-observed")
	readNotificationInbox(t, f, "inbox-owner")
	err := f.runtime.EditNamespace(notificationInboxTestNamespace("inbox-owner"), func(entries map[string]runtimekv.Entry) (bool, error) {
		state, err := decodeNotificationInboxState(entries["snapshot"], true, "inbox-owner")
		if err != nil {
			return false, err
		}
		original := state.Items[0]
		for index := 1; index < 1000; index++ {
			item := original
			item.OriginID = fmt.Sprintf("past-observed-growth-%04d", index)
			item.ID = notificationInboxID(item.OriginID)
			state.Items = append(state.Items, item)
		}
		state.Revision++
		encoded, err := json.Marshal(state)
		if err != nil {
			return false, err
		}
		// Long but valid past source IDs can fill the checkpoint map before
		// its 4096-count limit. No task, lease or delivery fact is fabricated.
		position := transcriptstore.NotificationSourcePosition{Incarnation: "past-fixture-incarnation", Publication: 1}
		positionJSON, _ := json.Marshal(position)
		length := len(encoded)
		for index := 0; index < 4094; index++ {
			key := fmt.Sprintf("past-growth-%04d-", index) + strings.Repeat("x", 880)
			keyJSON, _ := json.Marshal(key)
			growth := len(keyJSON) + len(positionJSON) + 2
			if length+growth > notificationInboxMaxBytes-(20<<10) {
				break
			}
			state.Positions[key] = position
			length += growth
		}
		encoded, err = json.Marshal(state)
		if err != nil || len(encoded) > notificationInboxMaxBytes-4096 || len(state.Positions) > 4096 {
			t.Fatalf("invalid bounded snapshot fixture: bytes=%d positions=%d err=%v", len(encoded), len(state.Positions), err)
		}
		marked := state
		marked.Items = append([]notificationInboxRecord(nil), state.Items...)
		for index := range marked.Items {
			marked.Items[index].ReadAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		readJSON, _ := json.Marshal(marked)
		if len(readJSON) <= notificationInboxMaxBytes {
			t.Fatal("fixture does not expose the read metadata expansion")
		}
		return true, storeNotificationInbox(entries, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.complete(t, "inbox-owner", "b-growth-new")
	result := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if result["reconciliation_incomplete"] != true || result["incomplete_reason"] != "capacity" {
		t.Fatalf("accepted a snapshot with no room to mark its records read: incomplete=%v reason=%v", result["reconciliation_incomplete"], result["incomplete_reason"])
	}
	entry, found, err := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil || len(state.Items) != 1000 {
		t.Fatalf("capacity discarded observed items: count=%d err=%v", len(state.Items), err)
	}
	if _, advanced := state.Positions["stream-b-growth-new"]; advanced {
		t.Fatal("capacity advanced an unretained source checkpoint")
	}
}
