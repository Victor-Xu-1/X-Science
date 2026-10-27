package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolprogress"
)

func resourceTranscriptSnapshotForTest() *toolprogress.ResourceAdmissionSnapshot {
	return &toolprogress.ResourceAdmissionSnapshot{DecisionAt: "2026-01-02T03:04:05Z", Reason: toolprogress.ResourceAdmissionCapacity,
		CapacityAvailableBytes: 900, UnusedPromisedBytes: 600, NetAvailableBytes: 300, RequestedBytes: 400, RequiredBytes: 400}
}

func resourceTranscriptInputForTest() map[string]any {
	return map[string]any{"purpose": "synthetic capacity observation", "background": true}
}

func resourceTranscriptPayloadForTest(t *testing.T, phase string, update *toolprogress.Update) (map[string]any, []byte) {
	t.Helper()
	payload := map[string]any{"status": "running", "toolPhase": phase, "toolCallId": "resource-chain-call",
		"toolName": "bash", "toolInput": resourceTranscriptInputForTest(), "toolOriginAttempt": 1}
	if update != nil {
		payload["progress"] = publicToolProgressPayload(*update, time.Millisecond)
	}
	if phase == "completed" {
		payload["status"], payload["toolResult"] = "completed", map[string]any{"ok": true}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := transcriptPayloadObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return decoded, raw
}

func resourceHistoryContentForTest(t *testing.T, server *Server) map[string]any {
	t.Helper()
	page := transcriptToolHistoryPage(t, server, "/api/conversations/frame-resource-chain/messages?limit=10", "")
	items, _ := page["items"].([]any)
	for _, value := range items {
		message, _ := value.(map[string]any)
		content, _ := message["content"].(map[string]any)
		if message["type"] == "tool_call" && content["call_id"] == "resource-chain-call" {
			return content
		}
	}
	t.Fatal("canonical history did not retain the original tool row")
	return nil
}

func resourceSnapshotMapForTest(progress any) map[string]any {
	value, _ := progress.(map[string]any)
	snapshot, _ := value["resourceAdmission"].(map[string]any)
	return snapshot
}

func TestResourceAdmissionProgressTraversesCanonicalHistoryAndWebDelivery(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "project-resource-chain", "frame-resource-chain")
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{FrameID: "frame-resource-chain",
		MessageUUID: "resource-user", ClientMessageID: "resource-user-client", Text: "observe synthetic capacity"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", "frame-resource-chain")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "resource-chain-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatal(claimed, err)
	}
	var batch workspace.ToolCallBatch
	var items []workspace.ToolCallBatchItem
	rootPayload, err := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{map[string]any{
		"id": "resource-chain-call", "type": "function", "name": "bash", "arguments": resourceTranscriptInputForTest(),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimed.Claim, ClientMessageID: "resource-batch", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: rootPayload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			return transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{
				BatchID: batch.BatchID, CallCount: batch.CallCount,
			}}, nil
		},
	})
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	batch, err = store.ClaimToolCallBatch(ctx, workspace.ClaimToolCallBatchInput{
		Claim: claimed.Claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, startPayload := resourceTranscriptPayloadForTest(t, "start", nil)
	_, _, _, err = repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimed.Claim, ClientMessageID: "resource-start", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: startPayload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, _, err := store.StartToolCallBatchItemTx(ctx, tx, workspace.StartToolCallBatchItemInput{
				Claim: claimed.Claim, BatchID: batch.BatchID, Ordinal: items[0].Ordinal,
				ExpectedBatchStateVersion: batch.StateVersion, ExpectedItemStateVersion: items[0].StateVersion, StartedEvent: event,
			})
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(ctx, stream.FrameID)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	_, err = store.EnqueueTaskOperation(ctx, workspace.TaskOperation{
		Version: 1, ID: "resource-chain-operation", OwnerID: access.UserID, FrameID: access.Frame.ID,
		FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID,
		RootFrameIncarnationID: access.RootFrameIncarnationID, Tool: "bash", NotificationID: "resource-chain-result",
		Request: json.RawMessage(`{"purpose":"synthetic observation only"}`),
		ObservationAdmission: &transcriptstore.ToolOperationAdmission{
			Claim: claimed.Claim, CallID: "resource-chain-call", ToolName: "bash", BootID: "resource-chain-boot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatches, err := store.ClaimOutbox(ctx, workspace.ClaimOutboxInput{
		WorkerID: "resource-chain-owner", Topics: []string{workspace.TaskOperationOutboxTopic}, Limit: 1, Lease: time.Minute,
	})
	if err != nil || len(dispatches) != 1 {
		t.Fatal(dispatches, err)
	}
	state := newTranscriptToolHistoryState()
	var after int64
	consumeLatest := func() (transcriptToolHistoryAction, map[string]any, map[string]any) {
		snapshot, err := repo.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
		if err != nil {
			t.Fatal(err)
		}
		events, err := repo.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
			AfterPublicationSequence: after, ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 50,
		})
		if err != nil {
			t.Fatal(err)
		}
		var latest transcriptToolHistoryAction
		var data, payload map[string]any
		seen := false
		for _, projected := range events {
			value, err := transcriptPayloadObject(projected.ResolvedPayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			action, found, err := state.consume(projected, value, 0)
			if err != nil {
				t.Fatalf("actual canonical fact/history fold rejected public progress: %v", err)
			}
			if !found {
				continue
			}
			wire, found, err := transcriptWebToolStreamPayload(transcriptstore.DeliveryClaim{
				StreamUID: stream.UID, Event: projected.Event, ResolvedPayloadJSON: projected.ResolvedPayloadJSON, PublicationSeq: projected.Event.PublicationSeq,
			}, value, stream.SessionID, "resource-chain-boundary", nil)
			if err != nil || !found {
				t.Fatalf("actual web delivery seam rejected the same event: found=%t err=%v", found, err)
			}
			latest, payload, seen = action, value, true
			data, _ = wire["data"].(map[string]any)
		}
		after = snapshot.ThroughPublicationSequence
		if !seen {
			t.Fatal("real producer did not append a canonical tool event")
		}
		return latest, data, payload
	}
	initial, _, seed := consumeLatest()
	if !initial.fact.inputSet || seed["toolInput"] == nil {
		t.Fatal("original admitted operation did not bind its input", seed)
	}
	grant, finish := make(chan struct{}), make(chan struct{})
	heartbeatSeen, admittedSeen := false, false
	window, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = toolprogress.Observe(window, time.Millisecond, func(ctx context.Context) (bool, error) {
		toolprogress.Report(ctx, toolprogress.Update{Phase: "waiting_for_resources", Indeterminate: true, ResourceAdmission: resourceTranscriptSnapshotForTest()})
		select {
		case <-grant:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		admitted := resourceTranscriptSnapshotForTest()
		admitted.Reason, admitted.UnusedPromisedBytes, admitted.NetAvailableBytes, admitted.ReservedBytes = toolprogress.ResourceAdmissionGranted, 0, 900, 400
		toolprogress.Report(ctx, toolprogress.Update{Phase: "starting", Indeterminate: true, ResourceAdmission: admitted})
		select {
		case <-finish:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}, func(update *toolprogress.Update, elapsed time.Duration, _ int) {
		if update == nil {
			return
		}
		// This is the same production writer as observeTaskOperation: it owns
		// the original outbox lease and intentionally does not repeat toolInput.
		if err := store.AppendTaskOperationProgress(ctx, dispatches[0], publicToolProgressPayload(*update, elapsed)); err != nil {
			t.Fatal(err)
		}
		action, data, payload := consumeLatest()
		if _, present := payload["toolInput"]; present {
			t.Fatal("real detached heartbeat unexpectedly manufactured tool input", payload)
		}
		folded := resourceSnapshotMapForTest(action.fact.progress)
		if update.Phase == "waiting_for_resources" && update.ResourceAdmission == nil && !heartbeatSeen {
			if folded["reason"] != toolprogress.ResourceAdmissionCapacity || resourceSnapshotMapForTest(data["progress"]) != nil {
				t.Fatal("history did not retain the DTO omitted by the real Observe heartbeat", folded, data)
			}
			if snapshot := resourceSnapshotMapForTest(resourceHistoryContentForTest(t, server)["progress"]); snapshot["decisionAt"] != "2026-01-02T03:04:05Z" {
				t.Fatal("actual history refresh lost the initial decision", snapshot)
			}
			heartbeatSeen = true
			close(grant)
		}
		if update.ResourceAdmission != nil && update.ResourceAdmission.Reason == toolprogress.ResourceAdmissionGranted {
			if folded["reason"] != toolprogress.ResourceAdmissionGranted || resourceSnapshotMapForTest(data["progress"])["reservedBytes"] != float64(400) {
				t.Fatal("original serializer/live seam lost admitted evidence", folded, data)
			}
			admittedSeen = true
			close(finish)
		}
	})
	if err != nil || !heartbeatSeen || !admittedSeen {
		t.Fatal(heartbeatSeen, admittedSeen, err)
	}
	if snapshot := resourceSnapshotMapForTest(resourceHistoryContentForTest(t, server)["progress"]); snapshot["reason"] != toolprogress.ResourceAdmissionGranted {
		t.Fatal("refresh did not preserve admitted history", snapshot)
	}
	if err := store.SettleTaskOperation(ctx, dispatches[0], map[string]any{"status": "completed", "ok": true}); err != nil {
		t.Fatal(err)
	}
	completed, data, _ := consumeLatest()
	if resourceSnapshotMapForTest(completed.fact.progress) != nil || resourceSnapshotMapForTest(data["progress"]) != nil || resourceSnapshotMapForTest(resourceHistoryContentForTest(t, server)["progress"]) != nil {
		t.Fatal("terminal serializer or history retained stale capacity facts")
	}
}

func TestResourceAdmissionCanonicalProgressRejectsUnknownOrInconsistentFacts(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(map[string]any, map[string]any)
	}{
		{"outer unknown", func(p, _ map[string]any) { p["backendPID"] = 1 }},
		{"nested unknown", func(_, a map[string]any) { a["backendID"] = "private" }},
		{"missing fact", func(_, a map[string]any) { delete(a, "netAvailableBytes") }},
		{"fractional integer", func(_, a map[string]any) { a["unusedPromisedBytes"] = 1.5 }},
		{"unsafe integer", func(_, a map[string]any) { a["capacityAvailableBytes"] = float64(1 << 54) }},
		{"changed demand", func(_, a map[string]any) { a["requestedBytes"] = float64(401) }},
		{"wrong phase", func(p, _ map[string]any) { p["phase"] = "executing" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, _ := resourceTranscriptPayloadForTest(t, "progress-000001", &toolprogress.Update{Phase: "waiting_for_resources", ResourceAdmission: resourceTranscriptSnapshotForTest()})
			progress := payload["progress"].(map[string]any)
			test.alter(progress, progress["resourceAdmission"].(map[string]any))
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			attempt := int64(1)
			event := transcriptstore.Event{StreamUID: "frame:resource-strict", EventID: 1, PublicationSeq: 1,
				Source: transcriptstore.EventSourcePayload, Type: "runner_checkpoint", RunnerAttempt: &attempt}
			if _, _, err := transcriptToolHistoryFactFromEvent(transcriptstore.ProjectedEvent{Event: event, ResolvedPayloadJSON: raw}, payload); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatal("canonical parser accepted malformed/unknown progress", err)
			}
			if _, _, err := transcriptWebToolStreamPayload(transcriptstore.DeliveryClaim{StreamUID: event.StreamUID, Event: event}, payload, "resource-strict", "strict-boundary", nil); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatal("web delivery accepted data rejected by canonical history", err)
			}
		})
	}
}

func TestResourceAdmissionHistoryPreservesOnlyMatchingSourceScope(t *testing.T) {
	for _, test := range []struct {
		name, stream, phase string
		attempt, revision   int64
		wantSnapshot        bool
	}{
		{"same heartbeat", "frame:resource-scope", "waiting_for_resources", 1, 3, true},
		{"new phase", "frame:resource-scope", "executing", 1, 3, false},
		{"other stream", "frame:other-resource-scope", "waiting_for_resources", 1, 3, false},
		{"other actual attempt", "frame:resource-scope", "waiting_for_resources", 2, 3, false},
		{"older revision ignored", "frame:resource-scope", "executing", 1, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			consume := func(stream string, attempt, revision int64, phase string, update *toolprogress.Update) transcriptToolHistoryAction {
				payload, raw := resourceTranscriptPayloadForTest(t, phase, update)
				event := transcriptstore.ProjectedEvent{Event: transcriptstore.Event{StreamUID: stream, EventID: revision, PublicationSeq: revision,
					Type: "runner_checkpoint", Source: transcriptstore.EventSourcePayload, RunnerAttempt: &attempt}, ResolvedPayloadJSON: raw}
				action, found, err := state.consume(event, payload, 0)
				if err != nil || !found {
					t.Fatal(found, err)
				}
				return action
			}
			consume("frame:resource-scope", 1, 1, "start", nil)
			consume("frame:resource-scope", 1, 2, "progress-000001", &toolprogress.Update{Phase: "waiting_for_resources", ResourceAdmission: resourceTranscriptSnapshotForTest()})
			action := consume(test.stream, test.attempt, test.revision, "progress-000002", &toolprogress.Update{Phase: test.phase})
			if got := resourceSnapshotMapForTest(action.fact.progress) != nil; got != test.wantSnapshot {
				t.Fatalf("snapshot crossed phase/stream/attempt/revision: got=%t want=%t fact=%+v", got, test.wantSnapshot, action.fact)
			}
		})
	}
	for _, test := range []struct {
		name         string
		initialKnown bool
		nextInput    map[string]any
		wantConflict bool
	}{
		{name: "unknown input is not borrowed"},
		{name: "newly bound input does not retroactively authorize a snapshot", nextInput: resourceTranscriptInputForTest()},
		{name: "explicit input change still conflicts", initialKnown: true, nextInput: map[string]any{"purpose": "different synthetic input"}, wantConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			consume := func(revision int64, update *toolprogress.Update, input map[string]any) (transcriptToolHistoryAction, error) {
				phase := "start"
				if update != nil {
					phase = "progress-000001"
				}
				payload, _ := resourceTranscriptPayloadForTest(t, phase, update)
				if input == nil {
					delete(payload, "toolInput")
				} else {
					payload["toolInput"] = input
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				attempt := int64(1)
				event := transcriptstore.ProjectedEvent{Event: transcriptstore.Event{
					StreamUID: "frame:resource-input-scope", EventID: revision, PublicationSeq: revision,
					Type: "runner_checkpoint", Source: transcriptstore.EventSourcePayload, RunnerAttempt: &attempt,
				}, ResolvedPayloadJSON: raw}
				action, found, err := state.consume(event, payload, 0)
				if !found {
					t.Fatal("input-boundary fact was not recognized")
				}
				return action, err
			}
			var initialInput map[string]any
			if test.initialKnown {
				initialInput = resourceTranscriptInputForTest()
			}
			if _, err := consume(1, nil, initialInput); err != nil {
				t.Fatal(err)
			}
			if _, err := consume(2, &toolprogress.Update{Phase: "waiting_for_resources", ResourceAdmission: resourceTranscriptSnapshotForTest()}, initialInput); err != nil {
				t.Fatal(err)
			}
			action, err := consume(3, &toolprogress.Update{Phase: "waiting_for_resources"}, test.nextInput)
			if test.wantConflict {
				if !errors.Is(err, transcriptstore.ErrEventConflict) {
					t.Fatal("explicit input conflict was weakened", err)
				}
				return
			}
			if err != nil || resourceSnapshotMapForTest(action.fact.progress) != nil {
				t.Fatal("unknown prior input authorized historical capacity facts", action.fact, err)
			}
		})
	}
}
