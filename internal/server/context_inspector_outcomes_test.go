package server

import (
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestContextInspectorPreservesErrorAndLaterCompletionOutcomes(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	first := recorder.begin("first-model", agentruntime.ModelRequest{})
	recorder.finish(first, agentruntime.ModelResponse{}, nil)
	failed, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: f.claim,
		ClientMessageID: "inspect-error-outcome", Status: "failed", PayloadJSON: []byte(`{"detail":"controlled fixture failure"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), transcriptstore.AppendFrameInputResponseInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, FrameID: f.stream.FrameID,
			ClientMessageID: "inspect-error-retry-input", PayloadJSON: []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"inspect-retry","content":"continue"}]}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at)
		SELECT 'inspect-retry-dispatch',?,COALESCE(MAX(sequence),0)+1,'frame_resumed','{"dispatch":{"status":"claimed"}}',?
		FROM frame_events WHERE frame_id=?`, f.stream.FrameID, time.Now().UTC(), f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.store.TransitionCompatibilityFrameStatus(f.stream.FrameID, "failed", "processing"); err != nil || !changed {
		t.Fatalf("authorized fixture resume status changed=%t err=%v", changed, err)
	}
	retry, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		RunnerID: "frame-resume:inspect-retry-dispatch", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput})
	if err != nil || !retry.Claimed {
		t.Fatalf("authorized retry=%+v err=%v", retry, err)
	}
	run.Transcript.Claim = retry.Claim
	next := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	second := next.begin("second-model", agentruntime.ModelRequest{})
	next.finish(second, agentruntime.ModelResponse{}, nil)
	completed, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: retry.Claim,
		ClientMessageID: "inspect-completed-outcome", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)})
	if err != nil {
		t.Fatal(err)
	}
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	turns := inspection["turns"].([]any)
	if len(turns) != 1 || len(inspection["calls"].([]any)) != 2 {
		t.Fatalf("one admitted prompt lost exact call ownership: %+v", inspection)
	}
	turn := turns[0].(map[string]any)
	outcomes, ok := turn["outcomes"].([]any)
	if !ok || len(outcomes) != 2 {
		t.Fatalf("later completion hid prior error terminal: %+v", turn)
	}
	for index, expected := range []struct {
		status  string
		eventID int64
		callID  string
	}{
		{"failed", failed.EventID, first.RequestID}, {"completed", completed.EventID, second.RequestID},
	} {
		outcome := outcomes[index].(map[string]any)
		calls := outcome["callIds"].([]any)
		if outcome["status"] != expected.status || outcome["terminalEventId"] != float64(expected.eventID) || len(calls) != 1 || calls[0] != expected.callID {
			t.Fatalf("terminal row lost immutable identity/call ownership: %+v", outcome)
		}
	}
}

func TestContextInspectorQueuedBatchDoesNotInventTerminal(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	for _, id := range []string{"queued-a", "queued-b"} {
		if _, _, _, err := f.repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
			ClientMessageID: id, FrameEventID: id + "-event", MessageUUID: id + "-message", Text: "fixture queued input"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: f.claim, ClientMessageID: "inspect-batch-original", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	batch, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		RunnerID: "inspect-batch", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !batch.Claimed || batch.Claim.ClaimedInputRevision != 3 {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: batch.Claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	call := recorder.begin("batch-model", agentruntime.ModelRequest{})
	recorder.finish(call, agentruntime.ModelResponse{}, nil)
	if _, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: batch.Claim, ClientMessageID: "inspect-batch-finished", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	turns := inspection["turns"].([]any)
	if len(turns) != 3 {
		t.Fatalf("accepted ordinary inputs lost: %+v", inspection)
	}
	unattributed := turns[1].(map[string]any)
	primary := turns[2].(map[string]any)
	if unattributed["status"] != "unknown" || len(unattributed["outcomes"].([]any)) != 0 || len(unattributed["callIds"].([]any)) != 0 ||
		primary["status"] != "completed" || inspection["coverage"].(map[string]any)["association"] != "partial" {
		t.Fatalf("batch consumption invented a per-input completion or left consumed input queued: %+v", inspection)
	}
}
