package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextAbsentSelectionDoesNotInheritLiveBindings(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages",
		webConversationSendInput{Content: "Ordinary request without a reading selection.", LoadingID: uuid.NewString()}, f.stream.OwnerID)
	if response.Code != 202 {
		t.Fatalf("ordinary HTTP %d %s", response.Code, response.Body.String())
	}
	message := pdfReadingUserMessageForTest(t, f, webString(p3DecodeObject(t, response)["msg_id"]))
	refs, decodeErr := decodeUserArtifactReferences(message["artifactRefs"])
	if decodeErr != nil || mapValue(message["inputData"])[transcriptstore.PDFReadingSnapshotKey] != nil || len(refs) != 0 {
		t.Fatalf("absent selection inherited a live PDF: %#v", message)
	}
	latest, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || latest.Revision != current.Revision || len(latest.Bindings) != 1 {
		t.Fatalf("ordinary send changed bindings: %#v %v", latest, err)
	}
}

func TestPDFReadingContextProjectionKeepsFrozenCoordinatesAcrossUnlink(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages",
		webConversationSendInput{Content: "Explain this page.", LoadingID: uuid.NewString(), ReadingContext: readingSelectionForTest(current)}, f.stream.OwnerID)
	if response.Code != 202 {
		t.Fatalf("input HTTP %d %s", response.Code, response.Body.String())
	}
	messageID := webString(p3DecodeObject(t, response)["msg_id"])
	unlinkPDFReadingForTest(t, f, current)
	history, snapshot, err := f.server.projectTranscriptWebHistory(context.Background(), f.stream, f.stream.OwnerID, f.stream.SessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	ordinal := 0
	for index, message := range history {
		if message["msg_id"] == messageID {
			full, ordinal = message, index+1
		}
	}
	if full == nil {
		t.Fatal("reading input disappeared from history")
	}
	check := func(value any) {
		t.Helper()
		frozen, err := transcriptstore.DecodePDFReadingSnapshot(value)
		if err != nil || frozen.Revision != current.Revision || frozen.PageNumber != 2 || len(frozen.Bindings) != 1 ||
			frozen.Bindings[0].VersionID != current.Bindings[0].VersionID {
			t.Fatalf("projection read mutable context: %#v %v", frozen, err)
		}
	}
	check(full["pdf_reading_context"])
	coordinates := full["content"].(map[string]any)["xScience"].(map[string]any)
	visible := coordinates["messageIndex"].(int)
	events, err := f.repo.ListProjectedCoordinateEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var publication int64
	found := false
	for _, event := range events {
		if event.Event.ClientMessageID != messageID {
			continue
		}
		record, _, err := buildIncrementalTranscriptWebUserMessage(event, f.stream.SessionID, snapshot.BranchID, ordinal-1, visible)
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err = json.Unmarshal(record.MessageJSON, &message); err != nil {
			t.Fatal(err)
		}
		check(message["pdf_reading_context"])
		if message["msg_id"] != messageID || message["id"] != full["id"] || record.Ordinal != ordinal ||
			record.VisibleIndex == nil || *record.VisibleIndex != visible {
			t.Fatalf("reading projection changed coordinates: %#v", record)
		}
		found = true
		publication = event.Event.PublicationSeq
	}
	if !found {
		t.Fatal("incremental source missing")
	}
	if err = f.server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "message.userCreated" && event.Payload["msg_id"] == messageID {
			found = true
			check(event.Payload["pdf_reading_context"])
			if event.ID != fmt.Sprintf("transcript-web:%s:%d:user", f.stream.UID, publication) {
				t.Fatalf("live identity drift: %#v", event)
			}
		}
	}
	if !found {
		t.Fatal("live reading input missing")
	}
}

func TestPDFReadingContextInvalidationCommitsOnlyWithChangedCAS(t *testing.T) {
	f, current, body := newPDFReadingFixture(t)
	ctx := context.Background()
	count, err := f.store.CountOutboxEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("x-science:pdf-reading-change:"+current.Scope.FrameID+"\x00"+current.Scope.FrameIncarnationID+"\x00"+body.OperationID)).String()
	event, err := f.store.GetOutboxEvent(ctx, workspace.DeriveOutboxEventID(workspace.RealtimeOutboxTopic, id))
	if err != nil {
		t.Fatal(err)
	}
	var envelope workspace.RealtimeOutboxEnvelope
	if err = json.Unmarshal(event.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	payload := envelope.Event.Payload
	if envelope.Event.Type != "conversation.listChanged" || envelope.Event.UserID != f.stream.OwnerID ||
		payload["reading_context_changed"] != true || payload["frame_incarnation_id"] != current.Scope.FrameIncarnationID ||
		payload["root_frame_incarnation_id"] != current.Scope.RootFrameIncarnationID || payload["reading_context_revision"] != float64(current.Revision) {
		t.Fatalf("invalidation lacks committed identity: %#v", envelope)
	}
	for _, request := range []pdfReadingMutationBody{body, {Schema: body.Schema, OperationID: uuid.NewString(),
		PDFReadingExpected: readingExpectedForTest(current), Sources: body.Sources}} {
		response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", request, f.stream.OwnerID)
		if response.Code != 200 || p3DecodeObject(t, response)["changed"] != false {
			t.Fatalf("replay/no-op=%d %s", response.Code, response.Body.String())
		}
	}
	after, err := f.store.CountOutboxEvents(ctx)
	if err != nil || after != count {
		t.Fatalf("replay/no-op emitted another change: %d => %d %v", count, after, err)
	}
	unlinkPDFReadingForTest(t, f, current)
	after, err = f.store.CountOutboxEvents(ctx)
	if err != nil || after != count+1 {
		t.Fatalf("unlink lacks exact one invalidation: %d => %d %v", count, after, err)
	}
}
