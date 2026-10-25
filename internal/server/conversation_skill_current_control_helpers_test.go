package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func submitSkillControlHTTPInput(t *testing.T, f *agentSaveArtifactsFixture, id, text string, typed bool) string {
	t.Helper()
	branch, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"content": text, "loading_id": id,
		"session_options": map[string]any{
			"memory_mode": "off", "target_branch_id": branch.ActiveBranchID,
			"expected_branch_id": branch.ActiveBranchID, "expected_generation": branch.Generation,
		},
	}
	if typed {
		body["turn_intent"] = conversationSkillControlIntent
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", body, f.stream.OwnerID)
	if response.Code != http.StatusAccepted {
		t.Fatalf("HTTP input status=%d body=%s", response.Code, response.Body.String())
	}
	inputID, _ := p3DecodeObject(t, response)["msg_id"].(string)
	if inputID == "" {
		t.Fatal("accepted HTTP input has no canonical identity")
	}
	f.stream, err = f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return inputID
}

func assertCurrentSkillControlMessage(t *testing.T, messages []any, wantControl bool) {
	t.Helper()
	count, currentUserCount := 0, 0
	lastUser := ""
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		content, _ := message["content"].(string)
		count += strings.Count(content, conversationSkillContinuationInstruction)
		if message["role"] == "user" {
			lastUser = content
			if content == conversationSkillContinuationInstruction {
				currentUserCount++
			}
		}
		for _, sentinel := range []string{"INTERNAL_CONTROL_NOT_MODEL_USER", "OLD_HIDDEN_CONTROL", "CURRENT_CONTROL_SENTINEL"} {
			if strings.Contains(content, sentinel) {
				t.Errorf("raw hidden control leaked into provider messages: %q", sentinel)
			}
		}
	}
	if wantControl {
		if count != 1 || currentUserCount != 1 || lastUser != conversationSkillContinuationInstruction {
			t.Errorf("current action was not projected once as the last user continuation: total=%d user=%d last=%q", count, currentUserCount, lastUser)
		}
	} else if count != 0 || currentUserCount != 0 {
		t.Errorf("a historical control was replayed for an ordinary input: total=%d user=%d", count, currentUserCount)
	}
}

func assertSkillControlTaskScope(t *testing.T, f *agentSaveArtifactsFixture, run *sessionRunnerChatRun) {
	t.Helper()
	intent, found, err := f.repo.GetActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found || intent.Text != "Create scientific artifacts." {
		t.Fatalf("Save changed the durable scientific task: intent=%#v found=%t err=%v", intent, found, err)
	}
	if run.currentControl == nil || run.TaskIntent != conversationSkillContinuationInstruction ||
		run.TaskIntentID == intent.ID || run.TaskIntentRevision != run.currentControl.publicationSeq ||
		run.ResponseLanguage != intent.Language {
		t.Fatalf("current control did not own only its runtime scope: task=%q id=%q revision=%d language=%q", run.TaskIntent, run.TaskIntentID, run.TaskIntentRevision, run.ResponseLanguage)
	}
	boundary, required, err := f.server.sessionRunnerDurableTaskBoundary(context.Background(), run)
	if err != nil || !required || boundary != run.currentControl.clientMessageID {
		t.Fatalf("durable receipts did not use the admitted control boundary: %q %t %v", boundary, required, err)
	}
	if len(run.ContinuationArtifactReferences) != 0 {
		t.Fatalf("earlier scientific artifacts became Save deliverables: %#v", run.ContinuationArtifactReferences)
	}
}

func resumeSkillControlCheckpoint(t *testing.T, f *agentSaveArtifactsFixture, failed bool) *sessionRunnerChatRun {
	t.Helper()
	checkpoint, _, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "current-recovery", Phase: transcriptstore.RunnerPhasePlanning,
		Resumable: true, PayloadJSON: []byte(`{"status":"completed","detail":"Prepared current control."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedAttempt := f.claim.Attempt
	runnerID := "skill-control-recovered"
	if failed {
		expectedAttempt++
		// This is an otherwise recoverable completed segment. Without the
		// control guard the shortcut would bind it to the old scientific task.
		delta, err := json.Marshal(map[string]any{
			"text": "No settled reusable procedure; no Skill was published.", "delta_index": 1,
			"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, ""),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
			Claim: f.claim, ClientMessageID: "failed-save-delta", Type: "content_delta", Source: transcriptstore.EventSourcePayload,
			PayloadJSON: delta,
		}); err != nil {
			t.Fatal(err)
		}
		terminal, err := json.Marshal(map[string]any{
			"status": "failed", "detail": "runner completion reference integrity failed (unsupported_citations=1)",
			"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, ""),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
			Claim: f.claim, ClientMessageID: "failed-save", Status: "failed",
			PayloadJSON: terminal,
		}); err != nil {
			t.Fatal(err)
		}
		// A failed Frame cannot be reclaimed by an arbitrary checkpoint runner.
		// Exercise the normal explicit-resume transaction and its dedicated
		// dispatch owner instead of bypassing that production authority fence.
		if unauthorized, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
			StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: runnerID, TTL: time.Minute,
			ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
		}); err != nil || unauthorized.Claimed {
			t.Fatalf("terminal control accepted an unauthorized reclaim: %#v err=%v", unauthorized, err)
		}
		resume, err := f.store.ResumeCompatibilityFrameConversation(f.stream.FrameID, workspace.ResumeCompatibilityFrameInput{})
		if err != nil || resume.Event == nil || resume.ResumedFrameID != f.stream.FrameID {
			t.Fatalf("explicit control resume=%#v err=%v", resume, err)
		}
		dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("skill-control-recovery", time.Minute)
		if err != nil || !claimed || dispatch.ResumeEvent.ID != resume.Event.ID || dispatch.FrameID != f.stream.FrameID {
			t.Fatalf("control resume dispatch=%#v claimed=%t err=%v", dispatch, claimed, err)
		}
		runnerID = frameResumeRunnerID(dispatch.ResumeEvent.ID)
	} else if _, err := f.db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`,
		time.Now().UTC().Add(-time.Minute), f.stream.UID, f.claim.Attempt); err != nil {
		t.Fatal(err)
	}
	resumed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: runnerID, TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !resumed.Claimed || resumed.Claim.Attempt != expectedAttempt || resumed.Claim.ClaimedInputRevision != f.claim.ClaimedInputRevision {
		t.Fatalf("resume=%#v expected attempt=%d err=%v", resumed, expectedAttempt, err)
	}
	f.claim = resumed.Claim
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if failed {
		if _, found, err := f.server.loadTranscriptCompletionRecoveryCandidate(context.Background(), run.Transcript); err != nil || found {
			t.Fatalf("control candidate bypassed ordinary scoped recovery: found=%t err=%v", found, err)
		}
	}
	return run
}
