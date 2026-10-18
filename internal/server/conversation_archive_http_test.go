package server

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func archiveHTTPRequestBody(frame workspace.Frame, archived bool, revision int64) map[string]any {
	return map[string]any{
		"archived": archived, "expected_incarnation_id": frame.IncarnationID,
		"expected_presentation_revision": revision,
	}
}

func archiveHTTPState(t *testing.T, store *workspace.Store, frameID string) (workspace.Frame, workspace.FrameRuntimeMetadata) {
	t.Helper()
	frame, found, err := store.GetFrame(frameID)
	if err != nil || !found {
		t.Fatalf("frame found=%t err=%v", found, err)
	}
	metadata, _, err := store.GetFrameRuntimeMetadata(frameID)
	if err != nil {
		t.Fatal(err)
	}
	return frame, metadata
}

func TestConversationArchiveHTTPStrictCASAndRestorePreserveCanonicalState(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "archive-roundtrip")
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, workspace.FrameRuntimeMetadata{
		FrameID: frame.ID, ContextData: map[string]any{"runtime_owned": "retain", "draft": "unsent"},
	}); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := store.UpdateConversationPresentation(context.Background(), workspace.ConversationPresentationUpdate{
		OwnerID: "local", FrameID: frame.ID, IncarnationID: frame.IncarnationID,
		ExpectedRevision: &zero, Extra: map[string]any{"pinned": true},
	}); err != nil {
		t.Fatal(err)
	}
	before, beforeMetadata := archiveHTTPState(t, store, frame.ID)
	beforeEvents, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeStream, err := repo.GetStream(context.Background(), "frame:"+frame.ID, "local")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + frame.ID + "/archive"
	archive := archiveHTTPRequestBody(frame, true, 1)
	if response := p3JSONRequest(t, app, http.MethodPost, path, archive, ""); response.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", response.Code, response.Body.String())
	}
	_, archivedMetadata := archiveHTTPState(t, store, frame.ID)
	archivedExtra := archivedMetadata.ContextData["web_extra"].(map[string]any)
	if timestamp, ok := archivedExtra["archived_at"].(float64); !ok || timestamp <= 0 {
		t.Fatalf("missing durable archive timestamp: %#v", archivedExtra)
	}
	row := p3DecodeObject(t, p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+frame.ID, nil, ""))
	view := row["extra"].(map[string]any)
	if view["archived_at"] != archivedExtra["archived_at"] || view["presentation_revision"] != float64(2) || view["pinned"] != true {
		t.Fatalf("canonical archive projection=%#v", view)
	}
	// Archive has strict CAS even when a stale request asks for the current value.
	if response := p3JSONRequest(t, app, http.MethodPost, path, archive, ""); response.Code != http.StatusConflict {
		t.Fatalf("stale archive noop status=%d body=%s", response.Code, response.Body.String())
	}
	if response := p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(frame, true, 2), ""); response.Code != http.StatusOK {
		t.Fatalf("current archive noop status=%d body=%s", response.Code, response.Body.String())
	}
	_, nooped := archiveHTTPState(t, store, frame.ID)
	if !reflect.DeepEqual(nooped.ContextData, archivedMetadata.ContextData) {
		t.Fatalf("archive noop mutated durable state: %#v", nooped.ContextData)
	}
	if response := p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(frame, false, 2), ""); response.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", response.Code, response.Body.String())
	}
	for _, stale := range []map[string]any{archive, archiveHTTPRequestBody(frame, false, 2)} {
		if response := p3JSONRequest(t, app, http.MethodPost, path, stale, ""); response.Code != http.StatusConflict {
			t.Fatalf("old receipt changed restored identity: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	after, metadata := archiveHTTPState(t, store, frame.ID)
	extra := metadata.ContextData["web_extra"].(map[string]any)
	if timestamp, present := extra["archived_at"]; present && timestamp != nil && timestamp != float64(0) {
		t.Fatalf("restore retained archive marker: %#v", extra)
	}
	if extra["_presentation_revision"] != float64(3) || extra["pinned"] != true ||
		extra["pinned_at"] != beforeMetadata.ContextData["web_extra"].(map[string]any)["pinned_at"] ||
		metadata.ContextData["runtime_owned"] != "retain" || metadata.ContextData["draft"] != "unsent" ||
		!after.UpdatedAt.Equal(before.UpdatedAt) || after.Status != before.Status {
		t.Fatalf("archive/restore changed activity or unrelated state: frame=%#v metadata=%#v", after, metadata)
	}
	afterEvents, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil || !reflect.DeepEqual(afterEvents, beforeEvents) {
		t.Fatalf("history changed: before=%#v after=%#v err=%v", beforeEvents, afterEvents, err)
	}
	afterStream, err := repo.GetStream(context.Background(), beforeStream.UID, "local")
	if err != nil || !reflect.DeepEqual(beforeStream, afterStream) {
		t.Fatalf("restore admitted work: before=%#v after=%#v err=%v", beforeStream, afterStream, err)
	}
}

func TestConversationArchiveHTTPStrictBodyIdentityAndRootScope(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "archive-identity")
	path := "/api/conversations/" + frame.ID + "/archive"
	for _, test := range []struct {
		name string
		body map[string]any
	}{
		{"missing-archived", map[string]any{"expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0}},
		{"missing-revision", map[string]any{"archived": true, "expected_incarnation_id": frame.IncarnationID}},
		{"missing-incarnation", map[string]any{"archived": true, "expected_presentation_revision": 0}},
		{"string-boolean", map[string]any{"archived": "true", "expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0}},
		{"fractional-revision", map[string]any{"archived": true, "expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0.5}},
		{"negative-revision", map[string]any{"archived": true, "expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": -1}},
		{"unknown-field", map[string]any{"archived": true, "expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0, "force": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if response := p3JSONRequest(t, app, http.MethodPost, path, test.body, ""); response.Code != http.StatusBadRequest {
				t.Fatalf("invalid body status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if response := p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(frame, true, 0), "foreign-owner"); response.Code != http.StatusNotFound {
		t.Fatalf("foreign owner status=%d body=%s", response.Code, response.Body.String())
	}
	stale := frame
	stale.IncarnationID = "replaced-incarnation"
	if response := p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(stale, false, 0), ""); response.Code != http.StatusConflict {
		t.Fatalf("stale incarnation noop status=%d body=%s", response.Code, response.Body.String())
	}
	child, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "archive-child", ProjectID: frame.ProjectID, ParentFrameID: frame.ID,
		AgentName: "OPERON", Status: "completed", ConversationType: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+child.ID+"/archive", archiveHTTPRequestBody(child, true, 0), ""); response.Code != http.StatusConflict {
		t.Fatalf("child archive status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestConversationArchiveCannotBeInjectedByCreateOrPresentationPatch(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "archive-patch-injection")
	if response := p3JSONRequest(t, app, http.MethodPatch, "/api/conversations/"+frame.ID, map[string]any{
		"extra": map[string]any{"archived_at": int64(1)}, "merge_extra": true,
		"expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0,
	}, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("ordinary patch minted archive authority: status=%d body=%s", response.Code, response.Body.String())
	}
	response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations", map[string]any{
		"id": "e1e677cb-8878-49e2-a2d9-d2d63e8b0c6b", "name": "New conversation",
		"extra": map[string]any{"project_id": "presentation-project", "archived_at": int64(1), "_presentation_revision": 99},
	}, "")
	if response.Code == http.StatusBadRequest {
		return // Rejecting protected create fields is also a valid contract.
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	_, metadata := archiveHTTPState(t, store, "e1e677cb-8878-49e2-a2d9-d2d63e8b0c6b")
	extra, _ := metadata.ContextData["web_extra"].(map[string]any)
	if value, present := extra["archived_at"]; present && value != nil && value != float64(0) {
		t.Errorf("create minted archive authority: %#v", extra)
	}
	if revision, present := extra["_presentation_revision"]; present && revision != float64(0) {
		t.Errorf("create minted presentation revision: %#v", extra)
	}
}

func TestConversationArchiveHTTPRejectsUnclaimedAndLiveWork(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "unclaimed-input", true: "live-claim"}[live], func(t *testing.T) {
			app, store := newP3WebConversationServer(t)
			createP3Project(t, store, "presentation-project", "local")
			frame := presentationTestFrame(t, store, "archive-busy")
			repo, err := store.TranscriptRepository(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: "frame:" + frame.ID, OwnerID: "local", ClientMessageID: "pending-input",
				FrameEventID: "pending-frame-event", MessageUUID: "pending-input", Text: "Continue the existing task",
			}); err != nil || !created {
				t.Fatalf("append created=%t err=%v", created, err)
			}
			var claim transcriptstore.RunnerClaim
			if live {
				result, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
					StreamUID: "frame:" + frame.ID, OwnerID: "local", RunnerID: "archive-busy-runner",
					TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
				})
				if err != nil || !result.Claimed {
					t.Fatalf("claim=%#v err=%v", result, err)
				}
				claim = result.Claim
			}
			before, metadata := archiveHTTPState(t, store, frame.ID)
			response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/archive", archiveHTTPRequestBody(frame, true, 0), "")
			if response.Code != http.StatusConflict {
				t.Fatalf("busy archive status=%d body=%s", response.Code, response.Body.String())
			}
			after, afterMetadata := archiveHTTPState(t, store, frame.ID)
			if !reflect.DeepEqual(metadata, afterMetadata) || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected archive changed running state")
			}
			if live {
				if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim); err != nil {
					t.Fatalf("rejected archive cancelled live work: %v", err)
				}
			}
		})
	}
}
