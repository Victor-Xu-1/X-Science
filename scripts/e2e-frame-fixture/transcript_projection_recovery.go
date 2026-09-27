package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

// These phases use the existing durable writer. No provider or tool is executed;
// the browser controls when each real runner attempt is published.
func cancelTranscriptProjectionTool(store *workspace.Store, frameID string, input transcriptStreamFixtureRequest) (map[string]any, error) {
	ctx := context.Background()
	repository, _, err := transcriptFixtureRepositoryForRun(ctx, store, frameID, input.Run)
	if err != nil {
		return nil, err
	}
	for index, phase := range []string{"start", "waiting", "start"} {
		if err := appendProjectionFixtureTool(ctx, repository, input.Run.claim(), *input.Run, phase, fmt.Sprint(index)); err != nil {
			return nil, err
		}
	}
	if err := finishProjectionFixtureRunner(ctx, repository, input.Run.claim(), input.Run.RunID, "cancelled"); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "frameId": frameID, "eventCount": 4}, nil
}

func completeTranscriptProjectionTool(store *workspace.Store, frameID string, input transcriptStreamFixtureRequest) (map[string]any, error) {
	ctx := context.Background()
	repository, stream, err := transcriptFixtureRepositoryForRun(ctx, store, frameID, input.Run)
	if err != nil {
		return nil, err
	}
	previous, err := repository.GetRunnerRuntimeState(ctx, stream.UID, stream.OwnerID, input.Run.Attempt)
	if err != nil {
		return nil, err
	}
	if previous.Status != "cancelled" || previous.RunnerID != input.Run.RunnerID ||
		previous.RunnerID != "e2e-transcript-stream:"+input.Run.RunID || previous.ClaimedInputRevision != input.Run.ClaimedInputRevision {
		return nil, errors.New("projection recovery requires the matching cancelled fixture runner")
	}
	resume, err := store.ResumeCompatibilityFrameConversation(stream.RootFrameID, workspace.ResumeCompatibilityFrameInput{})
	if err != nil {
		return nil, err
	}
	if resume.Event == nil {
		return nil, errors.New("projection fixture resume did not publish an event")
	}
	dispatch, claimed, err := store.ClaimNextCompatibilityFrameResumeDispatch("e2e-projection:"+input.Run.RunID, transcriptFixtureRunnerTTL)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, errors.New("projection fixture resume dispatch was not available")
	}
	if dispatch.ResumeEvent.ID != resume.Event.ID || dispatch.FrameID != frameID {
		if _, _, err := store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
			ResumeEventID: dispatch.ResumeEvent.ID, ExpectedAttempt: dispatch.Attempt, ClaimToken: dispatch.ClaimToken,
			ReasonCode: "e2e_fixture_dispatch_mismatch",
		}); err != nil {
			return nil, fmt.Errorf("release non-target projection fixture dispatch: %w", err)
		}
		return nil, errors.New("projection fixture did not claim its exact resume dispatch")
	}
	resumed, err := repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID,
		TTL: transcriptFixtureRunnerTTL, ResumeSource: transcriptstore.ResumeSourceUserInput,
	})
	if err != nil {
		return nil, err
	}
	if !resumed.Claimed || resumed.Claim.Attempt <= input.Run.Attempt || resumed.Claim.ClaimedInputRevision != input.Run.ClaimedInputRevision {
		return nil, errors.New("projection fixture resume did not claim a later attempt of the same input")
	}
	for _, phase := range []string{"start", "completed"} {
		if err := appendProjectionFixtureTool(ctx, repository, resumed.Claim, *input.Run, phase, "resumed-"+phase); err != nil {
			return nil, err
		}
	}
	refs := make([]transcriptstore.ArtifactReferenceInput, len(input.ArtifactRefs))
	for index, ref := range input.ArtifactRefs {
		refs[index] = transcriptstore.ArtifactReferenceInput{ArtifactID: ref.ArtifactID, VersionID: ref.VersionID, Relation: transcriptstore.ArtifactRelationProduced}
	}
	payload, err := json.Marshal(map[string]any{"text": input.Text, "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if err != nil {
		return nil, err
	}
	if err := retryTranscriptFixtureContention(ctx, "append_projection_fixture_answer", func() error {
		_, _, _, appendErr := repository.AppendAssistantEventWithArtifacts(ctx, transcriptstore.AppendAssistantEventWithArtifactsInput{
			Claim: resumed.Claim, ClientMessageID: "e2e-projection-answer:" + input.Run.RunID,
			Source: transcriptstore.EventSourcePayload, PayloadJSON: payload, Destinations: []string{"ws"}, References: refs,
		})
		return appendErr
	}); err != nil {
		return nil, err
	}
	if err := finishProjectionFixtureRunner(ctx, repository, resumed.Claim, input.Run.RunID, "completed"); err != nil {
		return nil, err
	}
	if event, _, err := store.CompleteCompatibilityFrameResumeDispatch(workspace.CompleteCompatibilityFrameResumeDispatchInput{
		ResumeEventID: dispatch.ResumeEvent.ID, ExpectedAttempt: dispatch.Attempt, ClaimToken: dispatch.ClaimToken, Status: "completed",
	}); err != nil || event.Type != "frame_resume_dispatch_completed" {
		return nil, fmt.Errorf("complete projection fixture resume dispatch: type=%q err=%v", event.Type, err)
	}
	return map[string]any{"ok": true, "frameId": frameID, "eventCount": 4, "attempt": resumed.Claim.Attempt}, nil
}

func appendProjectionFixtureTool(ctx context.Context, repository *transcriptstore.Repository, claim transcriptstore.RunnerClaim, original transcriptStreamFixtureRun, phase, suffix string) error {
	status := "running"
	if phase == "completed" {
		status = "completed"
	} else if phase == "waiting" {
		status = "waiting"
	}
	payload := map[string]any{
		"status": status, "toolPhase": phase, "toolCallId": "e2e-projection-tool:" + original.RunID,
		"toolName": "python", "toolInput": map[string]any{"code": "prepare_recovery_report()"},
		"toolOriginAttempt": original.Attempt, "message": "准备恢复验证报告",
	}
	if phase == "completed" {
		payload["toolResult"] = "Recovery report prepared."
	}
	return appendTranscriptFixtureCheckpoint(ctx, repository, claim, "e2e-projection-tool:"+original.RunID+":"+suffix, transcriptstore.RunnerPhaseExecuting, payload)
}

func finishProjectionFixtureRunner(ctx context.Context, repository *transcriptstore.Repository, claim transcriptstore.RunnerClaim, runID, status string) error {
	payload := map[string]any{"status": status, "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")}
	if status == "cancelled" {
		payload["reason_code"] = "user_cancelled"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return retryTranscriptFixtureContention(ctx, "finish_projection_fixture_runner", func() error {
		_, _, _, finishErr := repository.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
			Claim: claim, ClientMessageID: "e2e-projection-finish:" + runID + ":" + status, Status: status,
			PayloadJSON: raw, Destinations: []string{"ws"},
		})
		return finishErr
	})
}
