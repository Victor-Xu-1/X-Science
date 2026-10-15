package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebDeliveryUsesSafeProviderFailureClassification(t *testing.T) {
	const private = "PRIVATE_SSE_PROVIDER_DETAIL"
	cases := []struct{ name, kind, detail, wantKind, wantPhase string }{
		{"typed_subscription", "subscription", private, "subscription", "paused"},
		{"legacy_subscription", "", `provider endpoint returned 400: {"error":{"code":"InvalidSubscription","message":"` + private + `"}}`, "subscription", "paused"},
		{"unproven_configuration_advice", "", "Configure or select a model, then continue this same task. " + private, "unavailable", "paused"},
		{"configuration_missing", "configuration_missing", sessionRunnerMissingModelConfigurationDetail, "configuration_missing", "waiting_input"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, repo, _ := newTranscriptWebFixture(t)
			const ownerID, projectID, frameID = "provider-owner", "provider-project", "provider-frame"
			seedTranscriptWebFrame(t, store, ownerID, projectID, frameID)
			stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
				UID: "frame:" + frameID, OwnerID: ownerID, ExternalID: frameID, SessionID: frameID,
				Kind: transcriptstore.StreamKindFrameRef, ProjectID: projectID, RootFrameID: frameID, FrameID: frameID, Epoch: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := repo.AppendUserEvent(ctx, transcriptstore.AppendUserEventInput{
				StreamUID: stream.UID, OwnerID: ownerID, ClientMessageID: "provider-user",
				PayloadJSON: []byte(`{"text":"Preserve the current task."}`), Destinations: []string{transcriptWebDestination},
			}); err != nil {
				t.Fatal(err)
			}
			claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
				StreamUID: stream.UID, OwnerID: ownerID, RunnerID: "provider-runner", TTL: time.Minute,
				ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claimed.Claimed {
				t.Fatalf("claim=%#v err=%v", claimed, err)
			}
			interrupted, err := repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
				Claim: claimed.Claim, ClientMessageID: "provider-interruption",
				ReasonCode: sessionRunnerModelProviderUnavailableReasonCode, ProviderFailureKind: test.kind,
				ResumeDetail: test.detail, Resumable: true, AutoResume: false, Destinations: []string{transcriptWebDestination},
			})
			if err != nil || !interrupted.Created {
				t.Fatalf("interrupt=%#v err=%v", interrupted, err)
			}
			original := append([]byte(nil), interrupted.Event.PayloadJSON...)
			server := &Server{workspaceStore: store, transcriptStore: repo, compatEvents: newCompatEventHub()}
			if err := server.drainTranscriptWebDeliveries(ctx); err != nil {
				t.Fatal(err)
			}
			var runtimeFound, frameFound bool
			for _, event := range transcriptWebEvents(t, store, ownerID) {
				raw, err := json.Marshal(event.Payload)
				if err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range []string{private, "InvalidSubscription", "provider endpoint returned 400"} {
					if strings.Contains(string(raw), forbidden) {
						t.Errorf("public %s leaked private provider diagnostic %q", event.Type, forbidden)
					}
				}
				if event.Type == "runtime.statusChanged" && event.Payload["reason_code"] == sessionRunnerModelProviderUnavailableReasonCode {
					runtimeFound = true
					if event.Payload["phase"] != test.wantPhase || event.Payload["runtime_provider_failure_kind"] != test.wantKind {
						t.Errorf("runtime=%#v want phase=%s kind=%s", event.Payload, test.wantPhase, test.wantKind)
					}
				}
				if event.Type == "frame_update" && event.Payload["runtime_interruption_reason"] == sessionRunnerModelProviderUnavailableReasonCode {
					frameFound = true
					if event.Payload["status"] != "paused" || event.Payload["runtime_provider_failure_kind"] != test.wantKind || len(event.Invalidations) == 0 {
						t.Errorf("frame=%#v invalidations=%#v want kind=%s", event.Payload, event.Invalidations, test.wantKind)
					}
				}
			}
			if !runtimeFound || !frameFound {
				t.Errorf("provider interruption events missing: runtime=%t frame=%t", runtimeFound, frameFound)
			}
			stored, found, err := repo.GetEventByClientMessageID(ctx, stream.UID, ownerID, "provider-interruption")
			if err != nil || !found || !bytes.Equal(stored.PayloadJSON, original) {
				t.Fatalf("delivery rewrote private checkpoint: found=%t err=%v", found, err)
			}
			pause, found, err := repo.LatestRunnerInterruption(ctx, stream.UID, ownerID)
			if err != nil || !found || pause.EventID != interrupted.Event.EventID || pause.ResumeDetail != test.detail ||
				pause.ProviderFailureKind != test.kind || pause.ReasonCode != sessionRunnerModelProviderUnavailableReasonCode || pause.AutoResume {
				t.Fatalf("delivery changed durable interruption: %#v found=%t err=%v", pause, found, err)
			}
		})
	}
}
