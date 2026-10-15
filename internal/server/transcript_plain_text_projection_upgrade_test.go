package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestTranscriptPlainTextPublicReadRebuildsV13EmptyBody(t *testing.T) {
	store, repository, db := newTranscriptWebFixture(t)
	const owner, project, frame = "local", "project-plain-text-cache", "frame-plain-text-cache"
	const sourceID, identity = "plain-array-answer", "payload-genesis:plain-array-answer"
	const wantText = "A retained 研究 finding."
	seedTranscriptWebFrame(t, store, owner, project, frame)
	if _, err := store.AppendFrameEvent(workspace.FrameEventInput{ID: sourceID, FrameID: frame, Type: "assistant_message",
		Payload: map[string]any{"_uuid": "legacy-plain-answer", "role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "A retained "}, map[string]any{"type": "text", "text": "研究 finding."},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := repository.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "stream-plain-text-cache", OwnerID: owner, ExternalID: frame, SessionID: frame,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: project, RootFrameID: frame, FrameID: frame, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, readModel, fence := newTranscriptWebReadModelServingServer(t, store, repository, db, stream)
	page := requireTranscriptWebReadModelPage(t, readModel, stream, fence)
	if len(page.Messages) != 1 || page.Messages[0].MessageID != identity {
		t.Fatalf("normal writer did not retain the canonical imported identity: %+v", page)
	}
	var cached map[string]any
	if err := json.Unmarshal(page.Messages[0].MessageJSON, &cached); err != nil || webMessageText(cached) != wantText {
		t.Fatalf("corrected parser did not supply the actual accepted array: message=%+v err=%v", cached, err)
	}
	var checkpoint transcriptWebProjectorCheckpointV1
	if err := json.Unmarshal(page.State.ProjectorStateJSON, &checkpoint); err != nil || len(checkpoint.Assistant.Attempts) != 0 ||
		checkpoint.EventChainSHA256 != page.State.SourceChainSHA256 {
		t.Fatalf("plain imported cache unexpectedly embeds an assistant body or has inconsistent source hashes: checkpoint=%+v err=%v", checkpoint, err)
	}
	sourceBefore, err := repository.ListProjectedEvents(t.Context(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: owner, Limit: 10,
	})
	if err != nil || len(sourceBefore) != 1 {
		t.Fatalf("immutable source before cache fixture: events=%+v err=%v", sourceBefore, err)
	}
	// Restore the legal v13 derived result, not the source: v13 retained this
	// identity/coordinate but rendered accepted text-block arrays as empty.
	// Its row hash changes with its body; the immutable event-chain and the
	// body-free imported-history checkpoint remain exactly the original ones.
	content, ok := cached["content"].(map[string]any)
	if !ok {
		t.Fatalf("canonical message has no text content: %+v", cached)
	}
	content["content"] = ""
	oldBody, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_web_messages SET message_json=?,message_sha256=?
		WHERE stream_uid=? AND branch_id=? AND message_id=?`, oldBody, transcriptstore.TranscriptWebSHA256(oldBody),
		stream.UID, fence.BranchID, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_web_projection_state SET projector_version=13
		WHERE stream_uid=? AND branch_id=?`, stream.UID, fence.BranchID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	oldFence, err := readModel.GetTranscriptWebProjectionUpgradeFence(t.Context(), owner, stream.UID, fence.BranchID)
	if err != nil || oldFence.StateProjectorVersion != 13 || oldFence.StateStatus != "ready" ||
		oldFence.StateProjectionRevision != fence.StateProjectionRevision || oldFence.StateSourceChainSHA256 != fence.StateSourceChainSHA256 ||
		oldFence.StateBranchGeneration != oldFence.BranchGeneration || oldFence.StateThroughPublicationSequence != oldFence.ThroughPublicationSequence ||
		oldFence.StateSourceRevision != oldFence.SourceRevision || oldFence.VisibleMessageCount != 1 {
		t.Fatalf("v13 fixture is not a consistent old ready cache: fence=%+v err=%v", oldFence, err)
	}
	var observedBody []byte
	var observedHash string
	if err := db.QueryRow(`SELECT message_json,message_sha256 FROM transcript_web_messages
		WHERE stream_uid=? AND branch_id=? AND message_id=?`, stream.UID, fence.BranchID, identity).
		Scan(&observedBody, &observedHash); err != nil || !bytes.Equal(observedBody, oldBody) || observedHash != transcriptstore.TranscriptWebSHA256(oldBody) {
		t.Fatalf("old cache body/hash fixture is inconsistent: hash=%s err=%v", observedHash, err)
	}

	response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+frame+"/messages", nil, owner)
	if response.Code != http.StatusOK {
		t.Fatalf("normal public read status=%d body=%s", response.Code, response.Body.String())
	}
	items := transcriptWebServingItems(t, p3DecodeObject(t, response), "items")
	if len(items) != 1 {
		t.Fatalf("public read changed message count: %+v", items)
	}
	message, _ := items[0].(map[string]any)
	if message["id"] != identity || webMessageText(message) != wantText {
		t.Fatalf("public request served the old empty v13 body instead of rebuilding the same canonical message: %+v", message)
	}
	location := compatJSONRequest(t, app.Handler(), http.MethodGet,
		"/api/frames/"+frame+"/messages/locate?uuid="+url.QueryEscape(identity), owner, nil, http.StatusOK)
	if location["uuid"] != identity || location["idx"] != float64(0) {
		t.Fatalf("catch-up changed the stable UUID/Locate coordinate: %+v", location)
	}
	after := requireTranscriptWebReadModelFence(t, readModel, stream, fence.BranchID)
	if after.StateProjectorVersion <= 13 || after.StateProjectorVersion != transcriptstore.TranscriptWebProjectorVersion ||
		after.StateStatus != "ready" || after.StateProjectionRevision <= oldFence.StateProjectionRevision ||
		after.StateSourceChainSHA256 != oldFence.StateSourceChainSHA256 || after.StateSourceRevision != oldFence.StateSourceRevision {
		t.Fatalf("public read did not perform the existing projector upgrade: before=%+v after=%+v", oldFence, after)
	}
	sourceAfter, err := repository.ListProjectedEvents(t.Context(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: owner, Limit: 10,
	})
	if err != nil || len(sourceAfter) != 1 || sourceAfter[0].Event.EventID != sourceBefore[0].Event.EventID ||
		sourceAfter[0].Event.ClientMessageID != sourceBefore[0].Event.ClientMessageID ||
		!bytes.Equal(sourceAfter[0].Event.PayloadJSON, sourceBefore[0].Event.PayloadJSON) ||
		!bytes.Equal(sourceAfter[0].ResolvedPayloadJSON, sourceBefore[0].ResolvedPayloadJSON) {
		t.Fatalf("derived-cache upgrade rewrote the accepted immutable source: before=%+v after=%+v err=%v", sourceBefore, sourceAfter, err)
	}
}
