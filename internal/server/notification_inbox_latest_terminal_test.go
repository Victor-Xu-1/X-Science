package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func appendNotificationInboxLongHistory(t *testing.T, f *notificationInboxFixture, claim transcriptstore.RunnerClaim, prefix string) {
	t.Helper()
	for index := 0; index < 260; index++ {
		if _, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
			Claim: claim, ClientMessageID: fmt.Sprintf("%s-%03d", prefix, index), Type: "assistant_message",
			Source: transcriptstore.EventSourcePayload, PayloadJSON: []byte(`{"text":"ordinary public response"}`),
			Destinations: []string{"ws"},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func finishNotificationInboxClaim(t *testing.T, f *notificationInboxFixture, claim transcriptstore.RunnerClaim, status string) transcriptstore.Event {
	t.Helper()
	event, _, created, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claim, ClientMessageID: fmt.Sprintf("finish-%s-%d", status, claim.Attempt), Status: status,
		PayloadJSON: []byte(fmt.Sprintf(`{"status":%q,"detail":"canonical terminal"}`, status)),
	})
	if err != nil || !created {
		t.Fatalf("real finish created=%t err=%v", created, err)
	}
	return event
}

func TestNotificationInboxLatestTerminalDoesNotWaitForHistoryPages(t *testing.T) {
	f := newNotificationInboxFixture(t)
	_, claim := f.begin(t, "inbox-owner", "a-long-source")
	appendNotificationInboxLongHistory(t, f, claim, "long")
	finishNotificationInboxClaim(t, f, claim, "completed")
	// A long first source must not hide a selected later source's latest outcome.
	f.complete(t, "inbox-owner", "b-short-source")
	first := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if first["reconciliation_incomplete"] != true || len(first["items"].([]any)) != 2 {
		t.Fatalf("latest canonical terminals wait for tool-history pages=%#v", first)
	}
	entry, found, err := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	position := state.Positions[claim.StreamUID]
	if err != nil || position.Publication != 128 || position.Through <= 128 ||
		state.AfterSession != "a-long-source" || !state.IncludeSession {
		t.Fatalf("latest lookup fabricated historical scan progress=%#v err=%v", state, err)
	}
	// Read state survives later discovery of the same event through pagination.
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all",
		map[string]any{"read_receipt": first["read_receipt"]}, "inbox-owner", http.StatusOK)
	page := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	for count := 0; page["reconciliation_incomplete"] == true && count < 3; count++ {
		page = memoryCompatJSON(t, f.app.Handler(), http.MethodGet,
			notificationInboxTestPath+"?cursor="+url.QueryEscape(page["next_cursor"].(string)), nil, "inbox-owner", http.StatusOK)
	}
	if page["reconciliation_incomplete"] != false || len(page["items"].([]any)) != 2 || page["unread_count"] != float64(0) {
		t.Fatalf("latest outcome duplicated or read state lost=%#v", page)
	}
}

func TestNotificationInboxLatestTerminalSurvivesFrozenHistoryBound(t *testing.T) {
	f := newNotificationInboxFixture(t)
	_, claim := f.begin(t, "inbox-owner", "running-source")
	appendNotificationInboxLongHistory(t, f, claim, "running")
	first := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if len(first["items"].([]any)) != 0 || first["reconciliation_incomplete"] != true {
		t.Fatalf("fixture must begin with pending history and no terminal=%#v", first)
	}
	finishNotificationInboxClaim(t, f, claim, "completed")
	second := memoryCompatJSON(t, f.app.Handler(), http.MethodGet,
		notificationInboxTestPath+"?cursor="+url.QueryEscape(first["next_cursor"].(string)), nil, "inbox-owner", http.StatusOK)
	if len(second["items"].([]any)) != 1 || second["reconciliation_incomplete"] != true {
		t.Fatalf("new terminal hidden behind earlier frozen history bound=%#v", second)
	}
	marked := memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all",
		map[string]any{"read_receipt": first["read_receipt"]}, "inbox-owner", http.StatusOK)
	if marked["unread_count"] != float64(1) {
		t.Fatal("old observed receipt consumed newly finished outcome")
	}
}

func TestNotificationInboxLatestReceiptDoesNotSearchBackPastCancellation(t *testing.T) {
	f := newNotificationInboxFixture(t)
	stream, first := f.begin(t, "inbox-owner", "cancelled-latest")
	appendNotificationInboxLongHistory(t, f, first, "before-old-finish")
	finishNotificationInboxClaim(t, f, first, "completed")
	f.drain(t, "inbox-owner")
	if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: "inbox-owner", ClientMessageID: "second-input", FrameEventID: "second-frame-input",
		MessageUUID: "second-message", Text: "A genuinely new input.", Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: "inbox-owner", RunnerID: "second-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("real second claim=%#v err=%v", claimed, err)
	}
	finishNotificationInboxClaim(t, f, claimed.Claim, "cancelled")
	page := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if page["reconciliation_incomplete"] != true || len(page["items"].([]any)) != 0 {
		t.Fatalf("latest cancelled receipt invented an earlier current completion=%#v", page)
	}
}

func TestNotificationInboxLatestTerminalValidatesCanonicalReceiptPayload(t *testing.T) {
	f := newNotificationInboxFixture(t)
	_, claim := f.begin(t, "inbox-owner", "invalid-terminal")
	appendNotificationInboxLongHistory(t, f, claim, "before-invalid")
	terminal := finishNotificationInboxClaim(t, f, claim, "completed")
	// Simulate persisted corruption after the real typed finish. Fast lookup must
	// reuse the canonical terminal validator, not trust a receipt status alone.
	if _, err := f.db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND event_id=?`,
		`{"status":"failed","detail":"corrupt projection"}`, claim.StreamUID, terminal.EventID); err != nil {
		t.Fatal(err)
	}
	page := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, "inbox-owner", http.StatusOK)
	if page["reconciliation_incomplete"] != true || page["incomplete_reason"] != "source_unavailable" || len(page["items"].([]any)) != 0 {
		t.Fatalf("latest terminal bypassed canonical status proof=%#v", page)
	}
	entry, found, _ := f.runtime.Get(notificationInboxTestNamespace("inbox-owner"), "snapshot")
	state, err := decodeNotificationInboxState(entry, found, "inbox-owner")
	if err != nil || len(state.Positions) != 0 {
		t.Fatalf("failed latest validation advanced the historical cursor=%#v err=%v", state.Positions, err)
	}
}
