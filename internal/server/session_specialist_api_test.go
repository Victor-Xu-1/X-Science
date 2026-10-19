package server

import (
	"encoding/json"
	"net/http"
	"testing"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

const specialistFixtureOwner = "specialist-fixture-owner"

func newSessionSpecialistAPIFixture(t *testing.T) (*Server, *workspace.Store, workspace.Frame) {
	t.Helper()
	app, store := newP3WebConversationServer(t)
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The general P3 fixture does not install the transcript authority; this
	// endpoint deliberately requires the same authority as real admissions.
	app.transcriptStore = repo
	createP3Project(t, store, "specialist-fixture-project", specialistFixtureOwner)
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: "specialist-fixture-frame", ProjectID: "specialist-fixture-project", AgentName: "OPERON", Status: "completed", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	seedPermissionTestAgent(t, store, specialistFixtureOwner)
	if err := disableBundledMCPForLocalFixture(t.Context(), app, specialistFixtureOwner); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SYNTHETIC_A", "SYNTHETIC_B"} {
		if _, err := store.CreateAgent(workspace.CreateAgentInput{ID: "profile-" + name, UserID: specialistFixtureOwner, Name: name, DisplayName: name,
			Description: "Synthetic specialist protocol fixture.", SystemPrompt: "Immutable fixture prompt for " + name}); err != nil {
			t.Fatal(err)
		}
	}
	return app, store, frame
}

func specialistReadbackFixture(t *testing.T, app *Server, frame string) sessionagent.Readback {
	t.Helper()
	payload := memoryCompatJSON(t, app.Handler(), http.MethodGet, "/api/frames/"+frame+"/specialist", nil, specialistFixtureOwner, http.StatusOK)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var result sessionagent.Readback
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func specialistPrepareFixture(t *testing.T, app *Server, frame, name, commandID string, useDefault bool) sessionagent.Mutation {
	t.Helper()
	state := specialistReadbackFixture(t, app, frame)
	command := sessionagent.Command{Schema: sessionagent.CommandSchema, CommandID: commandID, Scope: state.Scope,
		ExpectedRevision: state.Revision, Branch: state.Branch, DesiredAgent: name, UseDefault: useDefault}
	payload := memoryCompatJSON(t, app.Handler(), http.MethodPost, "/api/frames/"+frame+"/specialist", command, specialistFixtureOwner, http.StatusOK)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var result sessionagent.Mutation
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSessionSpecialistAPIReadbackPrepareReplayAndDefault(t *testing.T) {
	app, store, frame := newSessionSpecialistAPIFixture(t)
	initial := specialistReadbackFixture(t, app, frame.ID)
	if initial.Phase != "legacy" || initial.Revision != 0 || initial.Applied.SelectionName != "OPERON" || !initial.Applied.IsDefault || initial.Applied.SnapshotID != "" || initial.AppliedUnavailable {
		t.Fatalf("legacy GET fabricated or lost actual identity: %#v", initial)
	}
	before, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	_ = specialistReadbackFixture(t, app, frame.ID)
	after, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil || len(before) != len(after) {
		t.Fatalf("legacy GET mutated private records: %v", err)
	}
	first := specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_A", "prepare-a", false)
	if first.Schema != sessionagent.MutationSchema || first.Replayed || first.Snapshot.Phase != "prepared" || first.Snapshot.Desired.SelectionName != "SYNTHETIC_A" || first.Snapshot.Applied.SelectionName != "OPERON" || first.Snapshot.Applied.SnapshotID != "" {
		t.Fatalf("prepared selection falsely replaced runtime readback: %#v", first)
	}
	useDefault := specialistPrepareFixture(t, app, frame.ID, "", "prepare-none", true)
	if !useDefault.Snapshot.Desired.IsDefault || useDefault.Snapshot.Desired.AssistantID != initial.Applied.AssistantID || useDefault.Snapshot.Revision != 2 {
		t.Fatalf("None was not resolved through exact server default: %#v", useDefault)
	}
	namedDefault := specialistPrepareFixture(t, app, frame.ID, initial.Applied.SelectionName, "prepare-named-default", false)
	if !namedDefault.Snapshot.Desired.IsDefault || namedDefault.Snapshot.Desired.SnapshotID != useDefault.Snapshot.Desired.SnapshotID || namedDefault.Receipt.Command == useDefault.Receipt.Command {
		t.Fatalf("named default did not resolve to the same definition with a distinct command: %#v", namedDefault)
	}
	payload := memoryCompatJSON(t, app.Handler(), http.MethodPost, "/api/frames/"+frame.ID+"/specialist", first.Receipt.Command, specialistFixtureOwner, http.StatusOK)
	raw, _ := json.Marshal(payload)
	var replay sessionagent.Mutation
	if err := json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Receipt != first.Receipt || replay.Snapshot.Revision != 3 || !replay.Snapshot.Desired.IsDefault {
		t.Fatalf("lost ACK replay rewound current selection: %#v", replay)
	}
	memoryCompatJSON(t, app.Handler(), http.MethodGet, "/api/frames/"+frame.ID+"/specialist", nil, "foreign-owner", http.StatusNotFound)
}
