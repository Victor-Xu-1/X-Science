package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

func TestSessionSpecialistCanonicalAdmissionConflictResponse(t *testing.T) {
	app, store, frame := newSessionSpecialistAPIFixture(t)
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "frame:" + frame.ID, OwnerID: specialistFixtureOwner, ExternalID: frame.ID,
		SessionID: frame.ID, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_A", "race-prepare-a", false)
	reference := sessionagent.InputReference{
		Scope: first.Snapshot.Scope, Branch: first.Snapshot.PreparedBranch,
		Revision: first.Snapshot.Revision, SnapshotID: first.Snapshot.Desired.SnapshotID,
	}
	if snapshot, err := store.SessionSpecialistDefinition(t.Context(), specialistFixtureOwner, frame.ID, &reference); err != nil || snapshot == nil || snapshot.ID != reference.SnapshotID {
		t.Fatalf("preflight did not confirm the original definition: snapshot=%#v err=%v", snapshot, err)
	}
	compatFrame, found, err := store.GetCompatibilityFrame(frame.ID)
	if err != nil || !found {
		t.Fatalf("conversation frame found=%t err=%v", found, err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/"+frame.ID+"/messages", nil)
	request.Header.Set("X-Science-User-Id", specialistFixtureOwner)
	const requestText = "Synthetic admission race"
	intent, err := app.webFrozenMessageIntent(request, compatFrame, webConversationSendInput{
		Content: requestText, LoadingID: "specialist-admission-race",
		SessionOptions: map[string]any{sessionagent.InputReferenceKey: reference},
	})
	if err != nil || intent == nil {
		t.Fatalf("new input intent=%#v err=%v", intent, err)
	}
	// Another successful preparation wins after preflight but before the first
	// admission. This input has never been accepted; no replay is fabricated.
	_ = specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_B", "race-prepare-b", false)
	before, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	input := specialistInputFixture(first, stream, intent.InputMessageID)
	input.Text = requestText
	input.InputData["request"] = requestText
	input.MessageIntent = intent
	_, _, created, admissionErr := repo.AppendFrameUserEvent(t.Context(), input)
	if !errors.Is(admissionErr, sessionagent.ErrRevisionConflict) {
		t.Fatalf("canonical stale admission returned %v, want revision conflict", admissionErr)
	}
	after, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil || created || len(after) != len(before) {
		t.Fatalf("rejected admission changed events: created=%t before=%d after=%d err=%v", created, len(before), len(after), err)
	}
	if _, accepted, err := store.LookupFrameMessageIntentReplay(t.Context(), *intent); err != nil || accepted {
		t.Fatalf("rejected first input acquired an acceptance proof: accepted=%t err=%v", accepted, err)
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "canonical wrapped revision conflict",
			err:    fmt.Errorf("canonical admission rejected: %w", admissionErr),
			status: http.StatusConflict, code: sessionagent.ErrRevisionConflict.Error(),
		},
		{
			name: "invalid remains a bad request", err: fmt.Errorf("invalid reference: %w", sessionagent.ErrInvalid),
			status: http.StatusBadRequest,
		},
		{
			name: "explicit forbidden keeps its boundary",
			err: &webConversationRequestError{
				Status: http.StatusForbidden, Code: "SYNTHETIC_FORBIDDEN", Detail: "forbidden", Cause: admissionErr,
			},
			status: http.StatusForbidden, code: "SYNTHETIC_FORBIDDEN",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.writeWebConversationSubmissionError(response, request, compatFrame, intent, test.err)
			if response.Code != test.status {
				t.Fatalf("response status=%d body=%s, want %d", response.Code, response.Body.String(), test.status)
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			code, _ := payload["code"].(string)
			if code != test.code || response.Header().Get(webErrorCodeHeader) != test.code {
				t.Fatalf("response code=%q header=%q, want %q", code, response.Header().Get(webErrorCodeHeader), test.code)
			}
		})
	}
}
