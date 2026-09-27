package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestProjectionRecoveryFixtureUsesCancelledThenLegallyResumedDurableTool(t *testing.T) {
	store, repository, stream := transcriptFixtureTestStream(t)
	_, version, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: "recovery-report", ProjectID: stream.ProjectID, Name: "report.md", ContentType: "text/markdown",
		Content: bytes.NewBufferString("# Recovery report"), MaxBytes: 1024, RootFrameID: stream.RootFrameID, FrameID: stream.FrameID,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := beginTranscriptStreamFixture(store, stream.FrameID, "projection-run")
	if err != nil {
		t.Fatal(err)
	}
	run := response["run"].(transcriptStreamFixtureRun)
	input := transcriptStreamFixtureRequest{Run: &run, BatchID: run.RunID}
	tampered := run
	tampered.OwnerID = "another-owner"
	if _, err := cancelTranscriptProjectionTool(store, stream.FrameID, transcriptStreamFixtureRequest{Run: &tampered, BatchID: run.RunID}); err == nil {
		t.Fatal("tampered owner accepted")
	}
	if _, err := completeTranscriptProjectionTool(store, stream.FrameID, input); err == nil {
		t.Fatal("active runner accepted as cancelled")
	}
	if _, err := appendTranscriptFixtureDeltas(store, stream.FrameID, transcriptStreamFixtureRequest{Run: &run, BatchID: "prefix", Chunks: []string{"Visible prefix."}}); err != nil {
		t.Fatal(err)
	}
	if result, err := cancelTranscriptProjectionTool(store, stream.FrameID, input); err != nil || result["eventCount"] != 4 {
		t.Fatalf("cancel result=%v err=%v", result, err)
	}
	input.Text = "Final recovery answer."
	input.ArtifactRefs = []transcriptFixtureArtifactReference{{ArtifactID: "recovery-report", VersionID: version.ID}}
	completed, err := completeTranscriptProjectionTool(store, stream.FrameID, input)
	if err != nil {
		t.Fatal(err)
	}
	if completed["attempt"].(int64) <= run.Attempt {
		t.Fatal("resume did not advance the attempt")
	}
	events, err := repository.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var phases, terminals []string
	var finalCount int
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["toolCallId"] != nil {
			expectedStatus := "running"
			if payload["toolPhase"] == "waiting" {
				expectedStatus = "waiting"
			}
			if payload["toolPhase"] == "completed" {
				expectedStatus = "completed"
			}
			if payload["status"] != expectedStatus {
				t.Fatalf("invalid tool phase/status: %#v", payload)
			}
			if payload["toolCallId"] != "e2e-projection-tool:"+run.RunID || payload["toolOriginAttempt"] != float64(run.Attempt) ||
				!reflect.DeepEqual(payload["toolInput"], map[string]any{"code": "prepare_recovery_report()"}) {
				t.Fatalf("changed tool identity/input: %#v", payload)
			}
			phases = append(phases, payload["toolPhase"].(string))
		}
		if event.Event.Type == "runner_finished" {
			terminals = append(terminals, payload["status"].(string))
		}
		if event.Event.Type == "assistant_message" {
			finalCount++
			if payload["text"] != input.Text || len(event.ArtifactReferences) != 1 || event.ArtifactReferences[0].VersionID != version.ID {
				t.Fatalf("wrong final answer or refs: %#v", event)
			}
		}
	}
	if !reflect.DeepEqual(phases, []string{"start", "waiting", "start", "start", "completed"}) || !reflect.DeepEqual(terminals, []string{"cancelled", "completed"}) || finalCount != 1 {
		t.Fatalf("phases=%v terminals=%v finalCount=%d", phases, terminals, finalCount)
	}
	frame, found, err := store.GetFrame(stream.FrameID)
	if err != nil || !found || frame.Status != "completed" {
		t.Fatalf("frame status=%s found=%t err=%v", frame.Status, found, err)
	}
}

func TestProjectionRecoveryFixtureRejectsInvalidCompletionBodies(t *testing.T) {
	store, _, stream := transcriptFixtureTestStream(t)
	response, err := beginTranscriptStreamFixture(store, stream.FrameID, "contract-run")
	if err != nil {
		t.Fatal(err)
	}
	run := response["run"].(transcriptStreamFixtureRun)
	for _, mutate := range []func(*transcriptStreamFixtureRequest){
		func(v *transcriptStreamFixtureRequest) { v.Text = "" },
		func(v *transcriptStreamFixtureRequest) { v.Text = strings.Repeat("x", 4097) },
		func(v *transcriptStreamFixtureRequest) { v.Chunks = []string{"unexpected"} },
		func(v *transcriptStreamFixtureRequest) { v.Copy = &transcriptStreamingParityCopy{} },
		func(v *transcriptStreamFixtureRequest) { v.Recovery = &transcriptStreamingRecoveryCopy{} },
		func(v *transcriptStreamFixtureRequest) { v.ArtifactRefs = nil },
		func(v *transcriptStreamFixtureRequest) { v.ArtifactRefs = append(v.ArtifactRefs, v.ArtifactRefs[0]) },
		func(v *transcriptStreamFixtureRequest) { v.ArtifactRefs[0].VersionID = "../bad" },
		func(v *transcriptStreamFixtureRequest) { v.BatchID = "wrong-run" },
	} {
		input := transcriptStreamFixtureRequest{Run: &run, BatchID: run.RunID, Text: "Final.", ArtifactRefs: []transcriptFixtureArtifactReference{{ArtifactID: "a", VersionID: "v"}}}
		mutate(&input)
		if err := validateTranscriptStreamFixtureRequest(&fixtureRequest{Action: "complete-projection-tool", FrameID: stream.FrameID, Transcript: &input}); err == nil {
			t.Fatalf("invalid input accepted: %#v", input)
		}
	}
}

func TestProjectionRecoveryFixtureReleasesNonTargetResumeDispatch(t *testing.T) {
	store, _, stream := transcriptFixtureTestStream(t)
	if _, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "other-frame", ProjectID: "project", AgentName: "OPERON", Status: "cancelled", ConversationType: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	other, err := store.ResumeCompatibilityFrameConversation("other-frame", workspace.ResumeCompatibilityFrameInput{})
	if err != nil || other.Event == nil {
		t.Fatalf("other resume=%#v err=%v", other, err)
	}
	response, err := beginTranscriptStreamFixture(store, stream.FrameID, "mismatch-run")
	if err != nil {
		t.Fatal(err)
	}
	run := response["run"].(transcriptStreamFixtureRun)
	input := transcriptStreamFixtureRequest{Run: &run, BatchID: run.RunID}
	if _, err := cancelTranscriptProjectionTool(store, stream.FrameID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := completeTranscriptProjectionTool(store, stream.FrameID, input); err == nil || !strings.Contains(err.Error(), "exact resume dispatch") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	dispatch, found, err := store.GetCompatibilityFrameResumeDispatch(other.Event.ID)
	if err != nil || !found || dispatch.Status != "registered" || dispatch.ClaimToken != "" {
		t.Fatalf("non-target dispatch not released: %#v found=%t err=%v", dispatch, found, err)
	}
}
