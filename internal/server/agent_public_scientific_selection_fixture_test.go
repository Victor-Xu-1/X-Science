package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

type scientificSelectedSourceFixture struct {
	*agentSaveArtifactsFixture
	gateway   serverAgentRuntimeToolGateway
	call      agentruntime.ToolCall
	ctx       context.Context
	requests  *atomic.Int32
	result    agentruntime.ToolResult
	event     transcriptstore.ProjectedEvent
	selection nativeScientificSourceSelection
}

func newScientificSelectedSourceFixture(t *testing.T) scientificSelectedSourceFixture {
	t.Helper()
	var current *agentSaveArtifactsFixture
	var observeTransfer atomic.Bool
	boundary := make(chan error, 8)
	fixture, gateway, call, ctx, requests, _ := scientificAcquisitionGatewayFixture(t, true, func(request *http.Request) {
		if !observeTransfer.Load() {
			return
		}
		snapshot, err := current.repo.GetProjectionSnapshot(request.Context(), current.stream.UID, current.stream.OwnerID)
		if err != nil {
			boundary <- err
			return
		}
		events, err := current.repo.ListProjectedCoordinateEvents(request.Context(), transcriptstore.ListProjectedEventsInput{
			StreamUID: current.stream.UID, OwnerID: current.stream.OwnerID, BranchID: snapshot.BranchID,
			BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 100,
		})
		if err == nil {
			err = errors.New("ordinary source selection was not committed before transfer HTTP")
			for _, event := range events {
				var selection nativeScientificSourceSelection
				if json.Unmarshal(agentPublicScientificSourceEventPayload(event), &selection) == nil &&
					selection.Kind == nativeScientificSourceSelectionKind && selection.ToolPhase == nativeScientificSourceSelectionPhase {
					err = nil
				}
			}
		}
		boundary <- err
	})
	current = fixture
	appendScientificDeclaredFetchedSource(t, fixture, gateway, ctx)
	declareScientificFixtureTool(t, fixture, gateway, call, "Starting the download")
	observeTransfer.Store(true)
	result, err := gateway.Execute(ctx, call)
	observeTransfer.Store(false)
	if err != nil || mapValue(result.Value)["ok"] != true || requests.Load() != 2 {
		t.Fatalf("real ordinary source/download failed: result=%#v err=%v requests=%d", result.Value, err, requests.Load())
	}
	select {
	case err := <-boundary:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("missing real transfer boundary observation")
	}
	selected := scientificSelectedSourceFixture{agentSaveArtifactsFixture: fixture, gateway: gateway,
		call: call, ctx: ctx, requests: requests, result: result}
	_, events := scientificAcquisitionEvents(t, fixture)
	count, acquisitions := 0, 0
	for _, event := range events {
		var selection nativeScientificSourceSelection
		if json.Unmarshal(agentPublicScientificSourceEventPayload(event), &selection) != nil || selection.Schema != agentPublicScientificAcquisitionSchema {
			continue
		}
		if selection.Kind == nativeScientificSourceSelectionKind {
			selected.event, selected.selection = event, selection
			count++
		} else {
			acquisitions++
		}
	}
	if count != 1 || acquisitions != 0 || selected.selection.SourceToolCallID != "ordinary-real-fetch" ||
		selected.selection.SourceEventID <= 0 || !validSHA256Hex(selected.selection.SourcePayloadSHA256) {
		t.Fatalf("ordinary selection fabricated acquisition or lost its real source: selection=%#v count=%d acquisitions=%d", selected.selection, count, acquisitions)
	}
	return selected
}

func appendScientificDeclaredFetchedSource(t *testing.T, fixture *agentSaveArtifactsFixture, gateway serverAgentRuntimeToolGateway, ctx context.Context) {
	t.Helper()
	input := map[string]any{"url": "https://source.example.org/records.csv?release=one&part=1", "limit": nativeScientificSourceLimit}
	call := agentruntime.ToolCall{ID: "ordinary-real-fetch", Name: "web_fetch", Arguments: mustMarshalRawMessage(input)}
	declareScientificFixtureTool(t, fixture, gateway, call, "Inspecting a controlled source")
	result, err := gateway.Execute(ctx, call)
	if err != nil || !agentPublicScientificResultAuthorizesDownload(normalizeNativeScientificSourceResult(mustMarshalRawMessage(result.Value)), stringValue(input["url"])) {
		t.Fatalf("declared canonical source failed: result=%#v err=%v", result.Value, err)
	}
	if err := fixture.server.checkpointChatTool(SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, gateway.taskRun,
		"completed", "Inspected the controlled source", call.ID, "completed", map[string]any{
			"toolName": call.Name, "toolInput": json.RawMessage(call.Arguments), "toolResult": result.Value,
		}); err != nil {
		t.Fatal(err)
	}
}
