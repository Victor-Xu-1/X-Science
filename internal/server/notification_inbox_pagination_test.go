package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNotificationInboxBoundedHistoryContinuesAfterLatestTerminal(t *testing.T) {
	f := newNotificationInboxFixture(t)
	_, claim := f.begin(t, "inbox-owner", "long-source")
	for index := 0; index < 130; index++ {
		_, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
			Claim: claim, ClientMessageID: fmt.Sprintf("step-%03d", index), Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
			PayloadJSON: []byte(`{"text":"ordinary public response"}`), Destinations: []string{"ws"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claim, ClientMessageID: "terminal", Status: "completed", PayloadJSON: []byte(`{"status":"completed","detail":"finished"}`),
	}); err != nil {
		t.Fatal(err)
	}
	first := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if first["reconciliation_incomplete"] != true || len(first["items"].([]any)) != 1 || first["incomplete_reason"] != "more_sources" {
		t.Fatalf("latest terminal must not pretend historical scan complete=%#v", first)
	}
	entry, found, err := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil || state.Positions[claim.StreamUID].Publication != 128 {
		t.Fatalf("unbounded source scan=%#v err=%v", state.Positions, err)
	}
	cursor := first["next_cursor"].(string)
	path := notificationInboxTestPath + "?cursor=" + url.QueryEscape(cursor)
	memoryCompatJSON(t, f.app.Handler(), http.MethodGet, path, nil, "foreign-owner", http.StatusConflict)
	second := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, path, nil, "inbox-owner", http.StatusOK)
	if second["reconciliation_incomplete"] != false || len(second["items"].([]any)) != 1 {
		t.Fatalf("terminal lost or duplicated across history page=%#v", second)
	}
	memoryCompatJSON(t, f.app.Handler(), http.MethodGet, path, nil, "inbox-owner", http.StatusConflict)
	// The same canonical outcome was already visible in the first snapshot.
	// Encountering it later in history must not turn it into a new unread item.
	marked := memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all",
		map[string]any{"read_receipt": first["read_receipt"]}, "inbox-owner", http.StatusOK)
	if marked["unread_count"] != float64(0) || marked["schema"] != notificationInboxSchema {
		t.Fatalf("historical rediscovery replaced visible outcome=%#v", marked)
	}
}

func TestNotificationInboxSourceFailurePreservesItemsAndCheckpoint(t *testing.T) {
	f := newNotificationInboxFixture(t)
	f.complete(t, "inbox-owner", "a-visible")
	before := readNotificationInbox(t, f, "inbox-owner")
	f.begin(t, "inbox-owner", "b-malformed")
	if _, err := f.db.Exec(`UPDATE frame_runtime_metadata SET context_data=? WHERE frame_id=?`, `{"_pending_input_requests":"invalid"}`, "b-malformed"); err != nil {
		t.Fatal(err)
	}
	failed := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if failed["reconciliation_incomplete"] != true || failed["incomplete_reason"] != "source_unavailable" || len(failed["items"].([]any)) != 1 {
		t.Fatalf("bad source discarded old snapshot=%#v", failed)
	}
	entry, found, _ := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, skipped := state.Positions["stream-b-malformed"]; skipped || state.AfterSession != "" {
		t.Fatal("failed source checkpoint advanced")
	}
	if state.Items[0].ID != before.items[0]["id"] {
		t.Fatal("failed source replaced prior item")
	}
	if _, err := f.db.Exec(`UPDATE frame_runtime_metadata SET context_data='{}' WHERE frame_id=?`, "b-malformed"); err != nil {
		t.Fatal(err)
	}
	recovered := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if recovered["reconciliation_incomplete"] != false || len(recovered["items"].([]any)) != 1 {
		t.Fatalf("source repair not recoverable=%#v", recovered)
	}
}

func TestNotificationInboxSourceHeadPageDoesNotSkipLaterOwnerSources(t *testing.T) {
	f := newNotificationInboxFixture(t)
	for index := 0; index < 17; index++ {
		f.begin(t, "inbox-owner", fmt.Sprintf("head-%02d", index))
	}
	first := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if first["reconciliation_incomplete"] != true {
		t.Fatalf("source heads not bounded=%#v", first)
	}
	entry, found, _ := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil || len(state.Positions) != 16 {
		t.Fatalf("source head window=%d err=%v", len(state.Positions), err)
	}
	second := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath+"?cursor="+url.QueryEscape(first["next_cursor"].(string)), nil, "inbox-owner", http.StatusOK)
	if second["reconciliation_incomplete"] != false {
		t.Fatalf("source head continuation failed=%#v", second)
	}
	entry, found, _ = f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	state, err = decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil || len(state.Positions) != 17 {
		t.Fatalf("last source silently skipped=%#v err=%v", state.Positions, err)
	}
}
