package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestTranscriptToolHistoryResumedCancellationProjectsFinalArtifact(t *testing.T) {
	for _, oldQuarantine := range []bool{false, true} {
		t.Run(fmt.Sprintf("old_quarantine_%t", oldQuarantine), func(t *testing.T) {
			testTranscriptToolHistoryResumedFinalArtifact(t, oldQuarantine)
		})
	}
}

func testTranscriptToolHistoryResumedFinalArtifact(t *testing.T, oldQuarantine bool) {
	ctx := context.Background()
	store, repository, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner-resume", "project-resume", "frame-resume")
	artifact := seedTranscriptWebReadModelArtifact(t, store, db, "project-resume", "frame-resume",
		"artifact-resume", "result.txt", []byte("durable result"), "runner")
	stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "stream-resume", OwnerID: "owner-resume", ExternalID: "frame-resume", SessionID: "frame-resume",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-resume", RootFrameID: "frame-resume", FrameID: "frame-resume", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendUser := func(client string) {
		_, _, _, err := repository.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: client,
			FrameEventID: client + "-event", MessageUUID: client + "-message", Text: "continue the task",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	claim := func() transcriptstore.RunnerClaim {
		result, err := repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "resume-runner",
			TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
		})
		if err != nil || !result.Claimed {
			t.Fatalf("claim failed: %v", err)
		}
		return result.Claim
	}
	readModel := transcriptstore.NewWebReadModelRepository(db, db)
	server := &Server{workspaceStore: store, transcriptStore: repository, transcriptWebReadModel: readModel}
	appendUser("first-input")
	first := claim()
	appendRunnerToolCheckpoint(t, repository, first, "first-tool-start", map[string]any{
		"status": "running", "toolPhase": "start", "toolCallId": "call-resume", "toolName": "Read",
		"toolInput": map[string]any{"path": "result.txt"},
	})
	if _, _, _, err = repository.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: first, ClientMessageID: "first-cancel", Status: "cancelled",
		PayloadJSON: []byte(`{"status":"cancelled","reason_code":"user_cancelled"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	appendUser("resume-input")
	second := claim()
	if second.Attempt <= first.Attempt {
		t.Fatal("resume did not create a new execution attempt")
	}
	appendRunnerToolCheckpoint(t, repository, second, "resumed-tool-start", map[string]any{
		"status": "running", "toolPhase": "start", "toolCallId": "call-resume", "toolName": "Read",
		"toolInput": map[string]any{"path": "result.txt"}, "toolOriginAttempt": first.Attempt,
	})
	appendRunnerToolCheckpoint(t, repository, second, "resumed-tool-complete", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolCallId": "call-resume", "toolName": "Read",
		"toolInput": map[string]any{"path": "result.txt"}, "toolOriginAttempt": first.Attempt,
		"toolResult": map[string]any{"ok": true},
	})
	answer, _, _, err := repository.AppendAssistantEventWithArtifacts(ctx, transcriptstore.AppendAssistantEventWithArtifactsInput{
		Claim: second, ClientMessageID: "resumed-final-answer", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"text":"final durable answer","assistant_segment":{"version":1,"ordinal":1}}`),
		References:  []transcriptstore.ArtifactReferenceInput{{ArtifactID: artifact.artifactID, VersionID: artifact.versionID, Relation: transcriptstore.ArtifactRelationProduced}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = repository.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: second, ClientMessageID: "resumed-finish", Status: "completed",
		PayloadJSON: []byte(`{"status":"completed","assistant_segment":{"version":1,"ordinal":1}}`),
	}); err != nil {
		t.Fatal(err)
	}
	if oldQuarantine {
		// Reproduce the old derived prefix without touching canonical events.
		// Its source fence is already current, so only a projector upgrade can
		// make the regular delivery pass revisit the terminal conflict.
		if _, err := db.Exec(`UPDATE transcript_web_projection_state
			SET projector_version=?,status='quarantined',last_error_code='projection_source_conflict',
			through_publication_seq=(SELECT through_publication_seq FROM transcript_branch_heads WHERE stream_uid=?),
			source_revision=(SELECT source_revision FROM transcript_branch_heads WHERE stream_uid=?)
			WHERE stream_uid=?`, 11, stream.UID, stream.UID, stream.UID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM transcript_web_projection_dirty WHERE stream_uid=?`, stream.UID); err != nil {
			t.Fatal(err)
		}
	}
	messages, snapshot, err := server.projectTranscriptWebHistory(ctx, stream, stream.OwnerID, stream.SessionID, "")
	if err != nil {
		t.Fatalf("valid resumed history must project its final answer: %v", err)
	}
	assertTranscriptTextMessage(t, messages[len(messages)-1], "final durable answer")
	tool := transcriptToolMessageByCallID(t, messages, "call-resume")
	if tool["id"] != transcriptToolHistoryIdentity(stream.UID, first.Attempt, "call-resume") || tool["content"].(map[string]any)["status"] != "completed" {
		t.Fatal("resumed tool lost its durable identity or completed state")
	}
	if oldQuarantine {
		if _, err := server.runTranscriptWebDeliveryPass(ctx); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := server.runTranscriptWebReadModelCycle(ctx); err != nil {
			t.Fatal(err)
		}
	}
	fence := requireTranscriptWebReadModelFence(t, readModel, stream, snapshot.BranchID)
	if fence.StateStatus != "ready" || fence.StateArtifactReferenceCount != 1 {
		t.Fatalf("resumed projection status=%s artifact_refs=%d", fence.StateStatus, fence.StateArtifactReferenceCount)
	}
	page := requireTranscriptWebReadModelPage(t, readModel, stream, fence)
	assertTranscriptWebReadModelRunnerReference(t, page.Messages[len(page.Messages)-1], answer.EventID, second.Attempt, artifact)
	toolCount, answerCount := 0, 0
	for _, message := range page.Messages {
		var decoded map[string]any
		if err := json.Unmarshal(message.MessageJSON, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["type"] == "tool_call" {
			toolCount++
		}
		if content, ok := decoded["content"].(map[string]any); ok && content["content"] == "final durable answer" {
			answerCount++
		}
	}
	if toolCount != 1 || answerCount != 1 {
		t.Fatalf("duplicate or missing recovered records: tools=%d answers=%d", toolCount, answerCount)
	}
}

func resumedToolFact(t *testing.T, attempt, origin, sequence int64, status, phase string) transcriptstore.ProjectedEvent {
	t.Helper()
	payload := map[string]any{
		"status": status, "toolPhase": phase, "toolCallId": "call-resume", "toolName": "Read",
		"toolInput": map[string]any{"path": "result.txt"},
	}
	if origin > 0 {
		payload["toolOriginAttempt"] = origin
	}
	if status == "completed" || status == "failed" || status == "cancelled" {
		payload["toolResult"] = map[string]any{"status": status}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return transcriptstore.ProjectedEvent{Event: transcriptstore.Event{
		StreamUID: "stream-resume", EventID: sequence, PublicationSeq: sequence, ClientMessageID: fmt.Sprintf("checkpoint-%d", sequence),
		RunnerAttempt: &attempt, Type: "runner_checkpoint", Source: transcriptstore.EventSourcePayload,
	}, ResolvedPayloadJSON: raw}
}

func consumeResumedToolFact(t *testing.T, state *transcriptToolHistoryState, event transcriptstore.ProjectedEvent) (transcriptToolHistoryAction, error) {
	t.Helper()
	payload, err := transcriptPayloadObject(event.ResolvedPayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	action, _, err := state.consume(event, payload, len(state.byIdentity))
	return action, err
}

func TestTranscriptToolHistoryResumesOnlySyntheticSettlements(t *testing.T) {
	for _, terminal := range []string{"cancelled", "failed", "completed", "interrupted"} {
		t.Run(terminal, func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			original, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, "running", "start"))
			if err != nil {
				t.Fatal(err)
			}
			state.settleAttempt(1, terminal)
			resumed, err := consumeResumedToolFact(t, state, resumedToolFact(t, 2, 1, 2, "running", "start"))
			if err != nil || resumed.append || resumed.identity != original.identity || resumed.fact.settlement != "" {
				t.Fatalf("synthetic settlement did not resume: %v", err)
			}
			if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 1, 3, "completed", "completed")); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatal("late old attempt settled the resumed execution")
			}
			if len(state.settleAttempt(1, "cancelled")) != 0 {
				t.Fatal("old execution attempt settled the resumed tool")
			}
			if settled := state.settleAttempt(2, "cancelled"); len(settled) != 1 || settled[0].fact.status != "canceled" {
				t.Fatal("resumed execution attempt did not settle its original tool identity")
			}
			if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 1, 3, "running", "start")); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatal("late old attempt reopened the tool")
			}
			if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 3, 1, 4, "running", "start")); err != nil {
				t.Fatalf("second legitimate resume failed: %v", err)
			}
			if final, err := consumeResumedToolFact(t, state, resumedToolFact(t, 3, 1, 5, "completed", "completed")); err != nil || final.fact.status != "completed" {
				t.Fatalf("resumed terminal receipt failed: %v", err)
			}
		})
	}
}

func modifyResumedToolFact(t *testing.T, event transcriptstore.ProjectedEvent, modify func(map[string]any)) transcriptstore.ProjectedEvent {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	modify(payload)
	var err error
	event.ResolvedPayloadJSON, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestTranscriptToolHistoryActualCancellationWithoutResultIsFinal(t *testing.T) {
	state := newTranscriptToolHistoryState()
	if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, "running", "start")); err != nil {
		t.Fatal(err)
	}
	state.settleAttempt(1, "cancelled")
	receipt := modifyResumedToolFact(t, resumedToolFact(t, 1, 1, 2, "cancelled", "cancelled"), func(payload map[string]any) {
		delete(payload, "toolResult")
	})
	if _, err := consumeResumedToolFact(t, state, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 2, 1, 3, "running", "start")); !errors.Is(err, transcriptstore.ErrEventConflict) {
		t.Fatal("actual cancellation without a result was mistaken for an inferred settlement")
	}
}

func TestTranscriptToolHistoryRejectsChangedResumeIdentity(t *testing.T) {
	cases := map[string]func(map[string]any){
		"input":          func(payload map[string]any) { payload["toolInput"] = map[string]any{"path": "other.txt"} },
		"name":           func(payload map[string]any) { payload["toolName"] = "Write" },
		"parent":         func(payload map[string]any) { payload["outerToolCallId"] = "different-parent" },
		"future_origin":  func(payload map[string]any) { payload["toolOriginAttempt"] = 3 },
		"invalid_origin": func(payload map[string]any) { payload["toolOriginAttempt"] = 0 },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			original := modifyResumedToolFact(t, resumedToolFact(t, 1, 0, 1, "running", "start"), func(payload map[string]any) {
				payload["outerToolCallId"] = "original-parent"
			})
			if _, err := consumeResumedToolFact(t, state, original); err != nil {
				t.Fatal(err)
			}
			state.settleAttempt(1, "cancelled")
			if _, err := consumeResumedToolFact(t, state, modifyResumedToolFact(t, resumedToolFact(t, 2, 1, 2, "running", "start"), modify)); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatal("changed resume identity was accepted")
			}
		})
	}
}

func TestTranscriptToolHistoryRequiresANewerExplicitResumeOrigin(t *testing.T) {
	state := newTranscriptToolHistoryState()
	original, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, "running", "start"))
	if err != nil {
		t.Fatal(err)
	}
	state.settleAttempt(1, "cancelled")
	if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 1, 2, "running", "start")); !errors.Is(err, transcriptstore.ErrEventConflict) {
		t.Fatal("same-attempt event reopened a settled tool")
	}
	separate, err := consumeResumedToolFact(t, state, resumedToolFact(t, 2, 0, 3, "running", "start"))
	if err != nil || !separate.append || separate.identity == original.identity || state.byIdentity[original.identity].fact.status != "canceled" {
		t.Fatal("an event without explicit origin rewrote the old settled identity")
	}
}

func TestTranscriptToolHistoryExplicitOriginDoesNotUseLegacyFallback(t *testing.T) {
	for _, checkpoint := range []struct{ status, phase string }{{"completed", "completed"}, {"running", "approval_resumed"}} {
		t.Run(checkpoint.phase, func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			original, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, "running", "start"))
			if err != nil {
				t.Fatal(err)
			}
			different, err := consumeResumedToolFact(t, state, resumedToolFact(t, 3, 2, 2, checkpoint.status, checkpoint.phase))
			if err != nil || !different.append || different.identity == original.identity || different.fact.attempt != 2 ||
				state.byIdentity[original.identity].fact.status != "running" {
				t.Fatal("explicit different origin rewrote the original tool identity")
			}
		})
	}
}

func TestTranscriptToolHistoryLegacyReceiptKeepsItsOriginalIdentity(t *testing.T) {
	state := newTranscriptToolHistoryState()
	original, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, "running", "start"))
	if err != nil {
		t.Fatal(err)
	}
	final, err := consumeResumedToolFact(t, state, resumedToolFact(t, 2, 0, 2, "completed", "completed"))
	if err != nil || final.append || final.identity != original.identity || final.fact.status != "completed" || len(state.byIdentity) != 1 {
		t.Fatal("legacy receipt without an origin lost its original tool identity")
	}
}

func TestTranscriptToolHistoryExplicitOriginKeepsPrivateTerminalPrivate(t *testing.T) {
	for _, otherPublicOrigin := range []bool{false, true} {
		t.Run(fmt.Sprintf("other_public_origin_%t", otherPublicOrigin), func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			var original transcriptToolHistoryAction
			if otherPublicOrigin {
				var err error
				original, err = consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, "running", "start"))
				if err != nil {
					t.Fatal(err)
				}
			}
			before := len(state.byIdentity)
			private := modifyResumedToolFact(t, resumedToolFact(t, 3, 2, 2, "completed", "completed"), func(payload map[string]any) {
				payload["visibility"] = "provisional"
				payload["toolInput"] = map[string]any{"path": "private.txt"}
				payload["toolResult"] = map[string]any{"secret": "private result"}
			})
			action, err := consumeResumedToolFact(t, state, private)
			if err != nil || action.identity != "" || len(state.byIdentity) != before {
				t.Fatal("private terminal without its own public start became visible")
			}
			if otherPublicOrigin && state.byIdentity[original.identity].fact.status != "running" {
				t.Fatal("private terminal changed a different public origin")
			}
		})
	}
}

func TestTranscriptToolHistoryNeverReopensRealTerminalReceipts(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			state := newTranscriptToolHistoryState()
			if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 1, 0, 1, terminal, terminal)); err != nil {
				t.Fatal(err)
			}
			state.settleAttempt(1, "cancelled")
			if _, err := consumeResumedToolFact(t, state, resumedToolFact(t, 2, 1, 2, "running", "start")); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatal("real terminal receipt was reopened")
			}
		})
	}
}
