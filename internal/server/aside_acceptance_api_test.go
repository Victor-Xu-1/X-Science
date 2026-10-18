package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func newAsideAcceptanceFixture(t *testing.T) *agentSaveArtifactsFixture {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	enabled := true
	if _, err := f.store.CreateAgent(workspace.CreateAgentInput{
		ID: "aside-profile", UserID: f.stream.OwnerID, Name: "OPERON", DisplayName: "Aside assistant",
		Description: "Controlled branch acceptance fixture", Unrestricted: true, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "source-finished", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAsideAcceptanceSelectedBranchReplayAndReadback(t *testing.T) {
	for _, asSession := range []bool{false, true} {
		t.Run(fmt.Sprintf("session-%t", asSession), func(t *testing.T) {
			f := newAsideAcceptanceFixture(t)
			ctx := context.Background()
			base, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			fork, err := f.repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: base.ActiveBranchID,
				ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
				ClientMutationID: "aside-sibling", SourceClientMessageID: "save-user", SourceMessageIndex: 0, ReplacementText: "SIBLING_NOT_SELECTED",
				Destinations: []string{transcriptWebDestination},
			})
			if err != nil {
				t.Fatal(err)
			}
			const intent = "bb3ab5dd-8a6c-43ce-85ef-3821928e878e"
			path := "/api/frames/" + f.stream.FrameID + "/aside"
			request := map[string]any{"request": "Inspect the selected source.", "intent_id": intent, "as_session": asSession,
				"source_branch_id": base.ActiveBranchID, "expected_generation": fork.Generation}
			response := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
			if response.Code != http.StatusOK {
				t.Fatalf("create: %d %s", response.Code, response.Body.String())
			}
			accepted := p3DecodeObject(t, response)
			child := webString(accepted["frame_id"])
			if child == "" || accepted["accepted"] != true || accepted["input_message_id"] != intent || accepted["source_branch_id"] != base.ActiveBranchID || accepted["prefix_len"] != float64(1) {
				t.Fatalf("receipt=%#v", accepted)
			}
			stream, found, err := f.repo.GetFrameStreamBySession(ctx, f.stream.OwnerID, child)
			if err != nil || !found {
				t.Fatalf("child stream: %t %v", found, err)
			}
			events, err := f.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, Limit: 30})
			if err != nil {
				t.Fatal(err)
			}
			var history strings.Builder
			inputs := 0
			for _, event := range events {
				history.Write(event.ResolvedPayloadJSON)
				if event.Event.Type == "user_message" {
					inputs++
				}
			}
			if !strings.Contains(history.String(), "Create scientific artifacts.") || strings.Contains(history.String(), "SIBLING_NOT_SELECTED") || inputs != 1 {
				t.Fatalf("wrong branch/input: %s", history.String())
			}
			state, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
			if err != nil || state.ActiveBranchID != fork.BranchID || state.Generation != fork.Generation {
				t.Fatalf("source changed: %#v %v", state, err)
			}
			// Change live source generation after acceptance. A lost response retry
			// must resolve the durable receipt before checking this newer state.
			if _, err := f.repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: base.ActiveBranchID,
				ExpectedActiveBranchID: fork.BranchID, ExpectedGeneration: fork.Generation,
				ClientMutationID: "aside-newer", SourceClientMessageID: "save-user", SourceMessageIndex: 0, ReplacementText: "LATER_NOT_SELECTED",
				Destinations: []string{transcriptWebDestination},
			}); err != nil {
				t.Fatal(err)
			}
			again := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
			if again.Code != http.StatusOK {
				t.Fatalf("retry: %d %s", again.Code, again.Body.String())
			}
			replayed := p3DecodeObject(t, again)
			if replayed["frame_id"] != child || replayed["replayed"] != true || replayed["input_message_id"] != intent {
				t.Fatalf("replay=%#v", replayed)
			}
			read := p3JSONRequest(t, f.server, http.MethodGet, path+"?intent_id="+intent, nil, f.stream.OwnerID)
			if read.Code != http.StatusOK || p3DecodeObject(t, read)["frame_id"] != child {
				t.Fatalf("lookup: %s", read.Body.String())
			}
			for key, value := range map[string]any{"request": "Changed request", "model": "Changed model", "as_session": !asSession, "source_branch_id": fork.BranchID, "expected_generation": fork.Generation + 1} {
				conflict := copyMapAny(request)
				conflict[key] = value
				bad := p3JSONRequest(t, f.server, http.MethodPost, path, conflict, f.stream.OwnerID)
				if bad.Code != http.StatusConflict || p3DecodeObject(t, bad)["code"] != "ASIDE_INTENT_CONFLICT" {
					t.Fatalf("%s conflict: %d %s", key, bad.Code, bad.Body.String())
				}
			}
			var frames, messages int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE id<>?`, f.stream.FrameID).Scan(&frames); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, stream.UID).Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if frames != 1 || messages != 1 {
				t.Fatalf("duplicate acceptance: children=%d inputs=%d", frames, messages)
			}
			reopened, err := workspace.Open(f.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recovered, found, err := reopened.GetCompatibilityAsideAcceptance(ctx, f.stream.OwnerID, f.stream.FrameID, intent)
			if err != nil || !found || recovered.Frame.ID != child {
				t.Fatalf("restart receipt=%#v %t %v", recovered, found, err)
			}
		})
	}
}

func TestAsideAcceptanceInheritsSelectedBranchRuntimeConfig(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	ctx := context.Background()
	base, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	selectedConfig := map[string]any{"model": "selected-model", "effort": "high", "gpu_mode": "on", "memory_mode": "on"}
	selected, err := f.repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: base.ActiveBranchID,
		ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "aside-config-selected", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
		ReplacementText: "Selected configuration source", RuntimeConfig: selectedConfig, Destinations: []string{transcriptWebDestination},
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := f.repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: selected.BranchID,
		ExpectedActiveBranchID: selected.BranchID, ExpectedGeneration: selected.Generation,
		ClientMutationID: "aside-config-active", SourceClientMessageID: selected.ReplacementEvent.ClientMessageID, SourceMessageIndex: 0,
		ReplacementText: "Different active configuration", Destinations: []string{transcriptWebDestination},
		RuntimeConfig: map[string]any{"model": "active-model", "effort": "low", "gpu_mode": "off", "memory_mode": "off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", map[string]any{
		"request": "Use the selected source configuration", "as_session": true,
		"intent_id":        "13bb35c1-ac87-46e5-bc2f-2060b3ae2b30",
		"source_branch_id": selected.BranchID, "expected_generation": active.Generation,
	}, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	child := webString(p3DecodeObject(t, response)["frame_id"])
	stream, found, err := f.repo.GetFrameStreamBySession(ctx, f.stream.OwnerID, child)
	if err != nil || !found {
		t.Fatalf("child stream: %t %v", found, err)
	}
	config, found, err := f.repo.LatestFrameRuntimeConfig(ctx, stream.UID, stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("child config: %#v %t %v", config, found, err)
	}
	for key, value := range selectedConfig {
		if config[key] != value {
			t.Fatalf("selected config %s=%v, child=%#v", key, value, config)
		}
	}
	state, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil || state.ActiveBranchID != active.BranchID || state.Generation != active.Generation {
		t.Fatalf("source changed: %#v %v", state, err)
	}
}
