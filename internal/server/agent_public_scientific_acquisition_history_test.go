package server

import (
	"encoding/json"
	"errors"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNativeScientificAcquisitionPublicProjection(t *testing.T) {
	fixture, gateway, parent, ctx, requests, _ := scientificAcquisitionFixture(t, true)
	result, err := gateway.Execute(ctx, parent)
	if err != nil || mapValue(result.Value)["ok"] != true || requests.Load() != 2 {
		t.Fatalf("real source and transfer failed before projection: %#v err=%v requests=%d", result.Value, err, requests.Load())
	}
	options := SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}
	if err := fixture.server.checkpointChatTool(options, gateway.taskRun, "completed", "Downloaded the source", parent.ID, "completed", map[string]any{
		"toolName": parent.Name, "toolInput": json.RawMessage(parent.Arguments), "toolResult": result.Value,
	}); err != nil {
		t.Fatal(err)
	}
	// A separate explicitly proposed web_fetch remains a public tool. The
	// internal source phase must never impersonate this ordinary lifecycle.
	public := agentruntime.ToolCall{ID: "visible-web-fetch", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://source.example.org/records.csv?release=one&part=1","limit":128}`)}
	if err := fixture.server.checkpointChatModelToolCalls(options, gateway.taskRun, []agentruntime.ToolCall{public}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointChatTool(options, gateway.taskRun, "running", "Inspecting a source", public.ID, "start", map[string]any{
		"toolName": public.Name, "toolInput": json.RawMessage(public.Arguments),
	}); err != nil {
		t.Fatal(err)
	}
	publicResult, err := gateway.Execute(ctx, public)
	if err != nil || requests.Load() != 3 {
		t.Fatalf("real public fetch failed: %#v err=%v requests=%d", publicResult.Value, err, requests.Load())
	}
	if err := fixture.server.checkpointChatTool(options, gateway.taskRun, "completed", "Inspected the source", public.ID, "completed", map[string]any{
		"toolName": public.Name, "toolInput": json.RawMessage(public.Arguments), "toolResult": publicResult.Value,
	}); err != nil {
		t.Fatal(err)
	}
	assertHistory := func(t *testing.T) {
		t.Helper()
		messages, _, active, err := fixture.server.loadActivatedTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID, "")
		if err != nil || !active {
			t.Fatalf("ordinary activated history rejected internal source facts: active=%t err=%v", active, err)
		}
		tools := map[string]map[string]any{}
		for _, message := range messages {
			if message["type"] == "tool_call" {
				content := mapValue(message["content"])
				tools[stringValue(content["call_id"])] = content
			}
		}
		if len(tools) != 2 || tools[parent.ID]["name"] != parent.Name || tools[parent.ID]["status"] != "completed" ||
			tools[public.ID]["name"] != public.Name || tools[public.ID]["status"] != "completed" {
			t.Fatalf("parent/public source lifecycle hidden or phantom source appeared: %#v", tools)
		}
	}
	t.Run("activated_history", assertHistory)
	t.Run("full_history_and_coordinates", func(t *testing.T) {
		if _, active, err := fixture.server.loadTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID); err != nil || !active {
			t.Fatalf("full history rejected source phase: active=%t err=%v", active, err)
		}
		if _, _, err := fixture.server.projectTranscriptWebCoordinates(t.Context(), fixture.stream, fixture.stream.OwnerID, fixture.stream.SessionID, ""); err != nil {
			t.Fatalf("coordinate parser rejected source phase: %v", err)
		}
	})
	t.Run("stream_delivery", func(t *testing.T) {
		_, events := scientificAcquisitionEvents(t, fixture)
		seen := 0
		for _, event := range events {
			var payload nativeScientificAcquisition
			_ = json.Unmarshal(event.Event.PayloadJSON, &payload)
			if payload.Schema != agentPublicScientificAcquisitionSchema {
				continue
			}
			seen++
			if !transcriptstore.IsRunnerInternalFact(event.Event, event.ResolvedPayloadJSON) {
				t.Error("runtime source phase was classified as public stream activity")
			}
			if err := fixture.server.publishTranscriptWebClaim(t.Context(), transcriptstore.DeliveryClaim{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, Event: event.Event,
				ResolvedPayloadJSON: event.ResolvedPayloadJSON,
			}); err != nil {
				t.Errorf("stream parser rejected internal source phase: %v", err)
			}
		}
		if seen != 2 {
			t.Fatalf("source lifecycle facts=%d", seen)
		}
	})
	t.Run("damaged_internal_fact_stays_private_and_rejected", func(t *testing.T) {
		_, _, _, request := scientificAcquisitionParent(t, fixture, gateway, parent, ctx)
		raw := mustMarshalRawMessage(map[string]any{"schema": agentPublicScientificAcquisitionSchema, "sourceURL": request.SourceURL,
			"toolName": 42, "toolCallId": "damaged-native-source", "toolPhase": "unknown-internal-phase", "status": "bogus"})
		if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
			Claim: fixture.claim, ClientMessageID: "damaged-internal-source", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
		}); err != nil {
			t.Fatal(err)
		}
		assertHistory(t)
		if _, err := fixture.server.validateAgentPublicScientificSourceURL(ctx, fixture.stream.UID, fixture.stream.OwnerID, request); !errors.Is(err, errAgentPublicScientificFileSource) || errors.Is(err, errAgentPublicScientificSourceMissing) {
			t.Fatalf("damaged internal source became source authority or absence: %v", err)
		}
	})
}

func TestNativeScientificInternalFactPreservesOrdinaryErrors(t *testing.T) {
	fixture, _, _, _, _, _ := scientificAcquisitionFixture(t, true)
	raw := json.RawMessage(`{"toolName":"web_fetch","toolCallId":"ordinary-broken","toolPhase":"unknown-phase","status":"bogus"}`)
	if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: fixture.claim, ClientMessageID: "ordinary-broken", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.server.loadTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID); err == nil {
		t.Fatal("ordinary malformed tool checkpoint was hidden by native internal-fact handling")
	}
}

func TestNativeScientificSourceSelectionPublicProjection(t *testing.T) {
	fixture := newScientificSelectedSourceFixture(t)
	if err := fixture.server.checkpointChatTool(SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, fixture.gateway.taskRun,
		"completed", "Downloaded the synthetic source", fixture.call.ID, "completed", map[string]any{
			"toolName": fixture.call.Name, "toolInput": json.RawMessage(fixture.call.Arguments), "toolResult": fixture.result.Value,
		}); err != nil {
		t.Fatal(err)
	}
	messages, _, active, err := fixture.server.loadActivatedTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID, "")
	if err != nil || !active {
		t.Fatalf("activated history rejected the selection variant: active=%t err=%v", active, err)
	}
	parentSeen := false
	for _, message := range messages {
		if message["type"] != "tool_call" {
			continue
		}
		content := mapValue(message["content"])
		if content["call_id"] == fixture.selection.SelectionID {
			t.Fatalf("private source selection became public activity: %#v", content)
		}
		if content["call_id"] == fixture.call.ID && content["name"] == fixture.call.Name && content["status"] == "completed" {
			parentSeen = true
		}
	}
	if !parentSeen {
		t.Fatal("selection hid the real completed parent download")
	}
	if _, active, err := fixture.server.loadTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID); err != nil || !active {
		t.Fatalf("full history rejected selection: active=%t err=%v", active, err)
	}
	if _, _, err := fixture.server.projectTranscriptWebCoordinates(t.Context(), fixture.stream, fixture.stream.OwnerID, fixture.stream.SessionID, ""); err != nil {
		t.Fatalf("coordinate consumer rejected selection: %v", err)
	}
	if !transcriptstore.IsRunnerInternalFact(fixture.event.Event, fixture.event.ResolvedPayloadJSON) {
		t.Fatal("selection variant was not private")
	}
	if err := fixture.server.publishTranscriptWebClaim(t.Context(), transcriptstore.DeliveryClaim{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, Event: fixture.event.Event,
		ResolvedPayloadJSON: fixture.event.ResolvedPayloadJSON,
	}); err != nil {
		t.Fatalf("stream consumer rejected selection: %v", err)
	}
}
