package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestAsideAcceptanceLookupScopesAbsenceAndStorageFailure(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "a106b559-377f-4db7-bbad-f185f8e652b0"
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	lookup := path + "?intent_id=" + intent
	absent := p3JSONRequest(t, f.server, http.MethodGet, lookup, nil, f.stream.OwnerID)
	if absent.Code != http.StatusOK || p3DecodeObject(t, absent)["accepted"] != false {
		t.Fatalf("absent: %s", absent.Body.String())
	}
	foreign := p3JSONRequest(t, f.server, http.MethodGet, lookup, nil, "other-owner")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d %s", foreign.Code, foreign.Body.String())
	}
	created := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"request": "Legacy compatible active source", "intent_id": intent}, f.stream.OwnerID)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %s", created.Body.String())
	}
	if _, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "other-parent", ProjectID: f.stream.ProjectID, AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	wrongParent := p3JSONRequest(t, f.server, http.MethodGet, "/api/frames/other-parent/aside?intent_id="+intent, nil, f.stream.OwnerID)
	if wrongParent.Code != http.StatusNotFound {
		t.Fatalf("wrong parent: %d %s", wrongParent.Code, wrongParent.Body.String())
	}
	if _, err := f.db.Exec(`UPDATE queued_user_messages SET payload='{}' WHERE intent_id=?`, intent); err != nil {
		t.Fatal(err)
	}
	legacy := p3JSONRequest(t, f.server, http.MethodGet, lookup, nil, f.stream.OwnerID)
	if legacy.Code != http.StatusConflict || p3DecodeObject(t, legacy)["code"] != "ASIDE_ACCEPTANCE_UNAVAILABLE" {
		t.Fatalf("legacy binding: %d %s", legacy.Code, legacy.Body.String())
	}
	if _, err := f.db.Exec(`DROP TABLE queued_user_messages`); err != nil {
		t.Fatal(err)
	}
	failed := p3JSONRequest(t, f.server, http.MethodGet, lookup, nil, f.stream.OwnerID)
	if failed.Code < 500 || strings.Contains(failed.Body.String(), `"accepted":false`) {
		t.Fatalf("storage failure became absence: %d %s", failed.Code, failed.Body.String())
	}
}

func TestAsideAcceptanceStaleSourceRollsBack(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	state, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", map[string]any{
		"request": "Must not create", "intent_id": "621af18c-a63d-49aa-8460-c81d30717b02", "source_branch_id": state.ActiveBranchID, "expected_generation": state.Generation + 1,
	}, f.stream.OwnerID)
	if response.Code != http.StatusConflict || p3DecodeObject(t, response)["code"] != "CONVERSATION_BRANCH_STATE_STALE" {
		t.Fatalf("stale: %d %s", response.Code, response.Body.String())
	}
	var frames, intents int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames`).Scan(&frames); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM queued_user_messages`).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if frames != 1 || intents != 0 {
		t.Fatalf("stale residue: frames=%d intents=%d", frames, intents)
	}
}
