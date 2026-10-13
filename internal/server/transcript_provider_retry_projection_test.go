package server

import (
	"context"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptProviderRetryProjectsLiveAndReloadFromSameAuthority(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	const ownerID, projectID, frameID = "retry-owner", "retry-project", "retry-frame"
	seedTranscriptWebFrame(t, store, ownerID, projectID, frameID)
	stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + frameID, OwnerID: ownerID, ExternalID: frameID, SessionID: frameID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: projectID, RootFrameID: frameID, FrameID: frameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.AppendUserEvent(context.Background(), transcriptstore.AppendUserEventInput{
		StreamUID: stream.UID, OwnerID: ownerID, ClientMessageID: "retry-user", PayloadJSON: []byte(`{"text":"test task"}`),
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: ownerID, RunnerID: "retry-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	interrupted, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: claimed.Claim, ClientMessageID: "provider-retry", ReasonCode: sessionRunnerModelProviderTemporaryReasonCode,
		ResumeDetail: "private diagnostics must not be projected", AutoResume: true, Resumable: true,
		Destinations: []string{transcriptWebDestination},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceStore: store, transcriptStore: repo, compatEvents: newCompatEventHub()}
	projection, _, err := server.compatibilityFrameRuntimeProjection(frameID)
	if err != nil || projection["runtime_retry_reason"] != sessionRunnerModelProviderTemporaryReasonCode || projection["runtime_paused"] == true {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	if projection["runtime_retry_attempt_id"] != transcriptAssistantMessageID(frameID, claimed.Claim.Attempt, 1) {
		t.Fatalf("retry projection is not bound to its canonical message-stream attempt: %#v", projection)
	}
	if err := server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	foundRetry := false
	for _, event := range transcriptWebEvents(t, store, ownerID) {
		if event.Type == "runtime.statusChanged" && event.Payload["reason_code"] == sessionRunnerModelProviderTemporaryReasonCode {
			foundRetry = event.Payload["phase"] == "waiting_for_lock" && event.Payload["message"] == nil
		}
		if event.Type == "frame_update" && event.Payload["status"] == "paused" {
			t.Fatal("automatic provider retry became a user-owned pause")
		}
	}
	if !foundRetry {
		t.Fatal("durable provider retry was dropped from the live invalidation lane")
	}
	resumed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: ownerID, RunnerID: "resumed-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !resumed.Claimed {
		t.Fatalf("resume=%#v err=%v", resumed, err)
	}
	projection, _, err = server.compatibilityFrameRuntimeProjection(frameID)
	if err != nil || projection["runtime_retry_reason"] != sessionRunnerModelProviderTemporaryReasonCode {
		t.Fatalf("request restart cleared retry without progress: %#v err=%v", projection, err)
	}
	if _, _, err := repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
		Claim: resumed.Claim, ClientMessageID: "public-recovery", Type: "content_delta", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"text":"The provider has returned public progress."}`),
	}); err != nil {
		t.Fatal(err)
	}
	projection, _, err = server.compatibilityFrameRuntimeProjection(frameID)
	if err != nil || projection["runtime_retry_reason"] != nil {
		t.Fatalf("recovered snapshot retained retry: %#v err=%v", projection, err)
	}
}

func TestTranscriptProviderRetryClassificationIsClosedAndAutomatic(t *testing.T) {
	for _, reason := range []string{sessionRunnerModelProviderTemporaryReasonCode, sessionRunnerProviderTransportTemporaryReasonCode} {
		if !transcriptWebProviderRetryInterruption(map[string]any{"status": "interrupted", "reason_code": reason, "auto_resume": true}) {
			t.Fatalf("missing automatic retry classification for %s", reason)
		}
		if transcriptWebProviderRetryInterruption(map[string]any{"status": "interrupted", "reason_code": reason, "auto_resume": false}) {
			t.Fatalf("manual pause classified as retry: %s", reason)
		}
	}
	for _, reason := range []string{"", "untrusted provider diagnostic", "model_provider_unavailable", "provider_output_token_limit"} {
		if publicProviderRetryReason(reason) != "" || transcriptWebProviderRetryInterruption(map[string]any{"status": "interrupted", "reason_code": reason, "auto_resume": true}) {
			t.Fatalf("unclassified reason exposed as automatic provider retry: %s", reason)
		}
	}
}
