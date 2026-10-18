package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const notificationInboxTestPath = "/api/notifications/inbox"

type notificationInboxFixture struct {
	store   *workspace.Store
	repo    *transcriptstore.Repository
	db      *sql.DB
	app     *Server
	runtime *runtimekv.Store
	root    string
}

func newNotificationInboxFixture(t *testing.T) *notificationInboxFixture {
	t.Helper()
	store, repo, db := newTranscriptWebFixture(t)
	f := &notificationInboxFixture{store: store, repo: repo, db: db, root: t.TempDir()}
	f.open()
	t.Cleanup(func() {
		if err := f.app.Close(context.Background()); err != nil {
			t.Error(err)
		}
		if err := f.runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *notificationInboxFixture) open() {
	f.runtime = runtimekv.New(filepath.Join(f.root, "runtime-state.sqlite"))
	f.app = New(Options{FileRoot: f.root, Workspace: f.store, Transcript: f.repo, RuntimeStore: f.runtime})
}

func (f *notificationInboxFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	f.open()
}

func (f *notificationInboxFixture) begin(t *testing.T, owner, frameID string) (transcriptstore.Stream, transcriptstore.RunnerClaim) {
	t.Helper()
	seedTranscriptWebFrame(t, f.store, owner, "project-"+frameID, frameID)
	stream, err := f.repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "stream-" + frameID, OwnerID: owner, ExternalID: frameID, SessionID: frameID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-" + frameID, RootFrameID: frameID, FrameID: frameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: owner, ClientMessageID: "input-" + frameID,
		FrameEventID: "frame-input-" + frameID, MessageUUID: "message-" + frameID,
		Text: "Complete a short test reply.", Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("input created=%t err=%v", created, err)
	}
	claimed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: owner, RunnerID: "runner-" + frameID, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("real claim=%#v err=%v", claimed, err)
	}
	return stream, claimed.Claim
}

func (f *notificationInboxFixture) complete(t *testing.T, owner, frameID string) transcriptstore.Event {
	t.Helper()
	_, claim := f.begin(t, owner, frameID)
	if _, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
		Claim: claim, ClientMessageID: "answer-" + frameID, Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"text":"A real canonical test response."}`), Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	event, _, created, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claim, ClientMessageID: "finish-" + frameID, Status: "completed",
		PayloadJSON: []byte(`{"status":"completed","detail":"A real canonical test response."}`),
	})
	if err != nil || !created {
		t.Fatalf("real finish created=%t err=%v", created, err)
	}
	f.drain(t, owner)
	return event
}

func (f *notificationInboxFixture) drain(t *testing.T, owner string) {
	t.Helper()
	if err := f.app.drainTranscriptWebOwner(context.Background(), owner, true); err != nil {
		t.Fatal(err)
	}
	state, err := f.repo.DeliverySettlementState(context.Background(), owner, "ws")
	if err != nil || state.Unsettled() {
		t.Fatalf("canonical web delivery blocked=%#v err=%v", state, err)
	}
}

type notificationInboxTestSnapshot struct {
	items    []map[string]any
	unread   int
	revision int64
	receipt  string
}

func readNotificationInbox(t *testing.T, f *notificationInboxFixture, owner string) notificationInboxTestSnapshot {
	t.Helper()
	raw := memoryCompatJSON(t, f.app.Handler(), http.MethodGet, notificationInboxTestPath, nil, owner, http.StatusOK)
	values, ok := raw["items"].([]any)
	unread, unreadOK := raw["unread_count"].(float64)
	revision, revisionOK := raw["revision"].(float64)
	receipt, receiptOK := raw["read_receipt"].(string)
	if raw["schema"] != "x-science.notification-inbox.v1" || !ok || !unreadOK || !revisionOK || !receiptOK || receipt == "" {
		t.Fatalf("invalid inbox snapshot=%#v", raw)
	}
	result := notificationInboxTestSnapshot{unread: int(unread), revision: int64(revision), receipt: receipt}
	for _, value := range values {
		item, ok := value.(map[string]any)
		id, idOK := item["id"].(string)
		origin, originOK := item["origin_id"].(string)
		if !ok || !idOK || !originOK || id == "" || origin == "" {
			t.Fatalf("invalid canonical notification=%#v", value)
		}
		result.items = append(result.items, item)
	}
	return result
}

func notificationInboxTestNamespace(owner string) string {
	digest := sha256.Sum256([]byte(owner))
	return "notification-inbox." + hex.EncodeToString(digest[:])
}
