package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolprogress"
)

// The worker is held without running code; SQLite, claims, batch/start identity,
// progress publication, and readback use their real APIs.
func TestRecoveredKernelProgressPublishesUnderNewClaimWithoutDuplicateLifecycle(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	options := SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}
	call := agentruntime.ToolCall{ID: "recovery-progress-call", Name: "python",
		Arguments: json.RawMessage(`{"code":"print(1)","environment":"science","memory_budget_mb":0}`)}
	if err := f.server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolCallID: call.ID, ToolName: call.Name, Arguments: string(call.Arguments),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolProgress, ToolCallID: call.ID, ToolName: call.Name, Arguments: string(call.Arguments), ProgressOrdinal: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "interrupted-progress-runner", Status: "failed", PayloadJSON: []byte(`{"status":"failed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	checkpoint, found, err := f.repo.LatestResumableCheckpoint(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("checkpoint found=%t err=%v", found, err)
	}
	// A real terminal attempt requires the normal Continue dispatch authority;
	// the presence of a resumable checkpoint alone cannot create a new owner.
	if _, err := f.store.ResumeCompatibilityFrameConversation(f.stream.RootFrameID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("progress-resume-dispatch", time.Minute)
	if err != nil || !found {
		t.Fatalf("resume dispatch found=%t err=%v", found, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID, TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("recovered claim=%+v err=%v", claimed, err)
	}
	recovered := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(claimed.Claim.Attempt), ClaimToken: claimed.Claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: claimed.Claim}}
	if err := f.server.bindRunnableToolCallBatches(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	options.RunnerID = claimed.Claim.RunnerID
	request, cancel := context.WithCancel(ctx)
	defer cancel()
	published, finished := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := observeRecoveredKernelTool(request, call, call.Arguments, func(observed context.Context) (map[string]any, error) {
			toolprogress.Report(observed, toolprogress.Update{Phase: "waiting_for_resources", Message: "Waiting for execution capacity", Indeterminate: true})
			<-observed.Done()
			return nil, context.Cause(observed)
		}, func(event agentruntime.Event) error {
			err := f.server.checkpointSessionRunnerToolEvent(request, options, recovered, event)
			select {
			case published <- err:
			default:
			}
			return sessionRunnerToolProgressPersistenceBestEffort(event, err)
		})
		finished <- err
	}()
	select {
	case err := <-published:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new recovery claim did not publish a running checkpoint")
	}
	wantID := transcriptRunnerClientMessageID(claimed.Claim, chatToolCheckpointPhase("progress-000001-running", call.ID))
	var payload []byte
	var attempt int64
	if err := f.db.QueryRow(`SELECT payload_json,runner_attempt FROM transcript_events WHERE stream_uid=? AND client_message_id=?`, f.stream.UID, wantID).Scan(&payload, &attempt); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	progress, _ := got["progress"].(map[string]any)
	input, _ := got["toolInput"].(map[string]any)
	if attempt != claimed.Claim.Attempt || got["toolOriginAttempt"] != float64(f.claim.Attempt) || got["toolCallId"] != call.ID || got["toolProgress"] != true || progress["phase"] != "waiting_for_resources" || input["memory_budget_mb"] != float64(0) {
		t.Fatalf("recovery progress lost original source/current claim/arguments: %+v", got)
	}
	var starts, progressCount, terminals int
	if err := f.db.QueryRow(`SELECT
		SUM(json_extract(payload_json,'$.toolPhase')='start'),
		SUM(json_extract(payload_json,'$.toolProgress')=1),
		SUM(json_extract(payload_json,'$.toolPhase') IN ('completed','failed','prestart_failed'))
		FROM transcript_events WHERE stream_uid=? AND json_extract(payload_json,'$.toolCallId')=?`, f.stream.UID, call.ID).Scan(&starts, &progressCount, &terminals); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || progressCount != 2 || terminals != 0 {
		t.Fatalf("lifecycle counts start=%d progress=%d terminal=%d", starts, progressCount, terminals)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("observation did not stop after request cancellation")
	}
}
