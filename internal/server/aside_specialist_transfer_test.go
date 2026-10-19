package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

func newAsideSpecialistFixture(t *testing.T) (*agentSaveArtifactsFixture, sessionagent.Mutation) {
	t.Helper()
	f := newAsideAcceptanceFixture(t)
	if err := disableBundledMCPForLocalFixture(t.Context(), f.server, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ASIDE_SYNTHETIC_A", "ASIDE_SYNTHETIC_B"} {
		if _, err := f.store.CreateAgent(workspace.CreateAgentInput{ID: "profile-" + name, UserID: f.stream.OwnerID,
			Name: name, DisplayName: name, Description: "Isolated atomic aside fixture", SystemPrompt: "FROZEN PRIVATE PROMPT " + name}); err != nil {
			t.Fatal(err)
		}
	}
	return f, prepareAsideSpecialist(t, f, "ASIDE_SYNTHETIC_A", "prepare-aside-source")
}

func TestAsideSpecialistCanonicalRunnerAppliesTransferredDefinition(t *testing.T) {
	f, prepared := newAsideSpecialistFixture(t)
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(input)
		if !strings.Contains(string(raw), "FROZEN PRIVATE PROMPT ASIDE_SYNTHETIC_A") {
			t.Error("runner did not use the transferred frozen profile")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Synthetic acknowledgement completed."}}]}`))
	}))
	defer provider.Close()
	body := asideSpecialistBody(prepared, "1a2d4dbe-154d-4a55-91b1-0b359bd42417")
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", body, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	child := decodeAsideReference(t, p3DecodeObject(t, response), "specialist_binding")
	result, err := f.server.RunSessionRunnerChatOnce(t.Context(), SessionRunnerChatOptions{SessionID: child.Scope.FrameID, RunnerID: "typed-aside-runner",
		Endpoint: provider.URL + "/v1/chat/completions", APIKey: "synthetic-fixture-key", Model: "synthetic-fixture-model", LeaseTTL: time.Minute,
		ReplayLimit: 100, OutputLimitBytes: 1 << 20, DisableSkillDiscovery: true})
	if err != nil || !result.Claimed || result.Status != "completed" || requests.Load() != 1 {
		t.Fatalf("runner=%#v requests=%d err=%v", result, requests.Load(), err)
	}
	state, err := f.store.SessionSpecialist(t.Context(), f.stream.OwnerID, child.Scope.FrameID)
	if err != nil || state.Phase != "applied" || state.Applied.SnapshotID != child.SnapshotID || state.Applied.SelectionName != prepared.Snapshot.Desired.SelectionName {
		t.Fatalf("resolver did not apply the actual child definition: %#v %v", state, err)
	}
}

func prepareAsideSpecialist(t *testing.T, f *agentSaveArtifactsFixture, name, id string) sessionagent.Mutation {
	t.Helper()
	path := "/api/frames/" + f.stream.FrameID + "/specialist"
	read := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	var state sessionagent.Readback
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &state) != nil {
		t.Fatalf("read specialist: %d %s", read.Code, read.Body.String())
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, path, sessionagent.Command{Schema: sessionagent.CommandSchema,
		CommandID: id, Scope: state.Scope, ExpectedRevision: state.Revision, Branch: state.Branch, DesiredAgent: name}, f.stream.OwnerID)
	var mutation sessionagent.Mutation
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &mutation) != nil {
		t.Fatalf("prepare specialist: %d %s", response.Code, response.Body.String())
	}
	return mutation
}

func asideSpecialistReference(mutation sessionagent.Mutation) sessionagent.InputReference {
	return sessionagent.InputReference{Scope: mutation.Snapshot.Scope, Branch: mutation.Snapshot.PreparedBranch,
		Revision: mutation.Snapshot.Revision, SnapshotID: mutation.Snapshot.Desired.SnapshotID}
}

func asideSpecialistBody(mutation sessionagent.Mutation, intent string) map[string]any {
	reference := asideSpecialistReference(mutation)
	return map[string]any{"request": "Synthetic immutable aside", "intent_id": intent,
		"source_branch_id": reference.Branch.ID, "expected_generation": reference.Branch.Generation, "source_specialist": reference}
}

func decodeAsideReference(t *testing.T, payload map[string]any, key string) sessionagent.InputReference {
	t.Helper()
	raw, err := json.Marshal(payload[key])
	var reference sessionagent.InputReference
	if err != nil || sessionagent.DecodeRecord(raw, &reference) != nil || !sessionagent.ValidScope(reference.Scope) || reference.Revision < 1 {
		t.Fatalf("missing actual %s proof: %#v", key, payload)
	}
	return reference
}

func TestAsideSpecialistAtomicTransferUsesChildBinding(t *testing.T) {
	for _, asSession := range []bool{false, true} {
		t.Run(fmt.Sprintf("session-%t", asSession), func(t *testing.T) {
			f, prepared := newAsideSpecialistFixture(t)
			body := asideSpecialistBody(prepared, "a9cfb4f0-a1ee-4d20-bfbd-f97f6a66bcb6")
			body["as_session"] = asSession
			path := "/api/frames/" + f.stream.FrameID + "/aside"
			response := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
			if response.Code != http.StatusOK {
				t.Fatalf("atomic create: %d %s", response.Code, response.Body.String())
			}
			accepted := p3DecodeObject(t, response)
			source, child := decodeAsideReference(t, accepted, "source_specialist"), decodeAsideReference(t, accepted, "specialist_binding")
			if source != asideSpecialistReference(prepared) || child.Scope.FrameID == source.Scope.FrameID || child.Scope.RootFrameID != child.Scope.FrameID || child.Scope.FrameID != webString(accepted["frame_id"]) || child.Scope.FrameIncarnationID == source.Scope.FrameIncarnationID || child.SnapshotID != source.SnapshotID {
				t.Fatalf("borrowed parent scope or changed definition: source=%#v child=%#v", source, child)
			}
			stream, found, err := f.repo.GetFrameStreamBySession(t.Context(), f.stream.OwnerID, child.Scope.FrameID)
			if err != nil || !found {
				t.Fatalf("child stream found=%t err=%v", found, err)
			}
			branch, err := f.repo.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || child.Branch.ID != branch.ActiveBranchID || child.Branch.Generation != branch.Generation {
				t.Fatalf("wrong child branch: %#v %v", child, err)
			}
			var inputs int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, stream.UID).Scan(&inputs); err != nil || inputs != 1 {
				t.Fatalf("first inputs=%d err=%v", inputs, err)
			}
			claim := specialistClaimFixture(t, f.repo, stream, "aside-child-runner")
			definition, err := f.repo.ClaimedRunnerAgentSnapshot(t.Context(), claim)
			if err != nil || definition == nil || definition.ID != source.SnapshotID || definition.SelectionName != prepared.Snapshot.Desired.SelectionName {
				t.Fatalf("child claim definition=%#v err=%v", definition, err)
			}
			state, err := f.store.SessionSpecialist(t.Context(), f.stream.OwnerID, child.Scope.FrameID)
			if err != nil || state.Phase != "prepared" {
				t.Fatalf("admission falsely applied worker: %#v %v", state, err)
			}
			read := p3JSONRequest(t, f.server, http.MethodGet, "/api/frames/"+child.Scope.FrameID+"/specialist", nil, f.stream.OwnerID)
			if asSession && read.Code != http.StatusOK || !asSession && read.Code != http.StatusNotFound {
				t.Fatalf("existing hidden-frame access boundary changed: %d %s", read.Code, read.Body.String())
			}
			if _, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claim, ClientMessageID: "aside-child-done", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`)}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
