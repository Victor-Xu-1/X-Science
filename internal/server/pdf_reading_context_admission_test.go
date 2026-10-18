package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextAcceptedQueueSurvivesUnlinkAndKeepsPage(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	appendSkillControlInput(t, f, "other-running-input", "Finish the current ordinary task.", false)
	claimSkillControlInput(t, f)
	request := webConversationSendInput{Content: "Explain the selected PDF page.", LoadingID: uuid.NewString(), ReadingContext: readingSelectionForTest(current)}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if response.Code != 202 {
		t.Fatalf("queue HTTP %d %s", response.Code, response.Body.String())
	}
	accepted := p3DecodeObject(t, response)
	messageID := webString(accepted["msg_id"])
	if receipt, _ := accepted["input_receipt"].(map[string]any); receipt["state"] != "queued" {
		t.Fatalf("not real queue acceptance: %#v", accepted)
	}
	removed := unlinkPDFReadingForTest(t, f, current)
	_, head, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: current.Bindings[0].ArtifactID, ProjectID: f.stream.ProjectID,
		Name: "reading.pdf", Kind: "pdf", Content: append(pdfReadingContextFixture(), []byte("\n% different head\n")...), CreatedBy: "fixture"})
	if err != nil || head.ID == current.Bindings[0].VersionID {
		t.Fatalf("new head=%#v err=%v", head, err)
	}
	retry := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if retry.Code != 202 || p3DecodeObject(t, retry)["msg_id"] != messageID {
		t.Fatalf("lost queue ACK HTTP %d %s", retry.Code, retry.Body.String())
	}
	finishSkillControlInput(t, f)
	delivered, err := f.server.recoverCompatibilityQueuedMessages()
	if err != nil || delivered != 1 {
		t.Fatalf("unlink blocked accepted queue: delivered=%d err=%v", delivered, err)
	}
	message := pdfReadingUserMessageForTest(t, f, messageID)
	input := mapValue(message["inputData"])
	frozen, err := transcriptstore.DecodePDFReadingSnapshot(input[transcriptstore.PDFReadingSnapshotKey])
	if err != nil || frozen.Revision != current.Revision || frozen.PageNumber != 2 || len(frozen.Bindings) != 1 ||
		frozen.Bindings[0].VersionID != current.Bindings[0].VersionID {
		t.Fatalf("queue snapshot drifted: %#v err=%v", frozen, err)
	}
	live, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || live.Revision != removed.Revision || len(live.Bindings) != 0 {
		t.Fatalf("drain recreated live binding: %#v %v", live, err)
	}
	text := runnerModelMessageText(message)
	if !strings.Contains(text, `"page_number":2`) || strings.Contains(text, head.ID) {
		t.Fatalf("provider did not receive frozen version/page: %s", text)
	}
	readers := inputAttachmentReaderStatesFromRunnerEntries([]eventjournal.Entry{{Message: message}})
	if len(readers) != 1 || readers[0].Read {
		t.Fatalf("page hint fabricated completed reading: %#v", readers)
	}
}

func TestPDFReadingContextImmediateCASAbsenceControlsAndEnvelopeForgery(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	selection := readingSelectionForTest(current)
	request := webConversationSendInput{Content: "Read this page.", LoadingID: uuid.NewString(), ReadingContext: selection}
	missingIdentity := request
	missingIdentity.LoadingID = ""
	if response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", missingIdentity, f.stream.OwnerID); response.Code != 400 {
		t.Fatalf("reading input lacks a frozen retry identity: %d %s", response.Code, response.Body.String())
	}
	removed := unlinkPDFReadingForTest(t, f, current)
	stale := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if stale.Code != 409 {
		t.Fatalf("stale immediate selection accepted: HTTP %d %s", stale.Code, stale.Body.String())
	}
	control := request
	control.TurnIntent = conversationSkillControlIntent
	denied := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", control, f.stream.OwnerID)
	if denied.Code != 400 {
		t.Fatalf("control inherited reading: %d %s", denied.Code, denied.Body.String())
	}
	// Possession of a JSON snapshot is not queue acceptance.
	snapshot, err := transcriptstore.FreezePDFReadingSelection(current, *selection)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	data := map[string]any{"request": "Forged frozen input", transcriptstore.PDFReadingSnapshotKey: value}
	_, _, _, err = f.store.QueueCompatibilityMessage(f.stream.FrameID, "forged-reading-envelope", data, map[string]any{"text": "Forged frozen input", "inputData": data})
	if err == nil {
		t.Fatal("caller-authored queue snapshot became authority")
	}
	_, _, _, err = f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "forged-reading-input", FrameEventID: "forged-reading-event",
		MessageUUID: "forged-reading-message", Text: "Forged frozen input", InputData: data})
	if err == nil {
		t.Fatal("raw append acquired reading authority")
	}
	ordinary := webConversationSendInput{Content: "Do not attach any reading context.", LoadingID: uuid.NewString()}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", ordinary, f.stream.OwnerID)
	if response.Code != 202 {
		t.Fatalf("ordinary send=%d %s", response.Code, response.Body.String())
	}
	message := pdfReadingUserMessageForTest(t, f, webString(p3DecodeObject(t, response)["msg_id"]))
	if mapValue(message["inputData"])[transcriptstore.PDFReadingSnapshotKey] != nil {
		t.Fatal("absent selection inherited live or retired bindings")
	}
	latest, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || latest.Revision != removed.Revision {
		t.Fatal("ordinary send rewrote PDF context")
	}
}

func pdfReadingUserMessageForTest(t *testing.T, f *agentSaveArtifactsFixture, messageID string) eventjournal.Message {
	t.Helper()
	events, err := f.repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event.ClientMessageID != messageID {
			continue
		}
		var message eventjournal.Message
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	t.Fatalf("canonical input %s missing", messageID)
	return nil
}

func TestPDFReadingContextBranchReceiptReplaysFrozenSelection(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	branch, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	request := webConversationSendInput{Content: "Read the selected page on this branch.", LoadingID: uuid.NewString(),
		ReadingContext: readingSelectionForTest(current), SessionOptions: map[string]any{
			"target_branch_id": branch.ActiveBranchID, "expected_branch_id": branch.ActiveBranchID, "expected_generation": branch.Generation,
		}}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if response.Code != 202 {
		t.Fatalf("branch input HTTP %d %s", response.Code, response.Body.String())
	}
	id := webString(p3DecodeObject(t, response)["msg_id"])
	unlinkPDFReadingForTest(t, f, current)
	replay := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if replay.Code != 202 || p3DecodeObject(t, replay)["msg_id"] != id {
		t.Fatalf("accepted branch input rechecked live context: %d %s", replay.Code, replay.Body.String())
	}
	changed := request
	selection := *request.ReadingContext
	selection.PageNumber = 1
	changed.ReadingContext = &selection
	conflict := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", changed, f.stream.OwnerID)
	if conflict.Code != 409 {
		t.Fatalf("same input ID changed reading page: %d %s", conflict.Code, conflict.Body.String())
	}
	message := pdfReadingUserMessageForTest(t, f, id)
	snapshot, err := transcriptstore.DecodePDFReadingSnapshot(mapValue(message["inputData"])[transcriptstore.PDFReadingSnapshotKey])
	if err != nil || snapshot.PageNumber != 2 || snapshot.Revision != current.Revision {
		t.Fatalf("branch snapshot changed: %#v %v", snapshot, err)
	}
}
