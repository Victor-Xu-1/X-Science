package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestRunnerProviderFailureKindRoundTripsWithoutChangingResumeAuthority(t *testing.T) {
	ctx := context.Background()
	repo, _, dsn := newTranscriptRepository(t)
	for _, kind := range []string{"configuration_missing", "subscription", "authentication", "access_denied", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			streamID := "provider-kind-" + kind
			seedTranscriptInput(t, repo, streamID, "owner-a")
			claimed, err := repo.ClaimRunner(ctx, ClaimRunnerInput{
				StreamUID: streamID, OwnerID: "owner-a", RunnerID: "provider-kind-runner", TTL: time.Minute, ResumeSource: ResumeSourceFresh,
			})
			if err != nil || !claimed.Claimed {
				t.Fatalf("claim=%#v err=%v", claimed, err)
			}
			cause := RunnerInterruptionCause{ReasonCode: "model_provider_unavailable", Detail: "private diagnostic retained"}
			input := InterruptRunnerInput{
				Claim: claimed.Claim, ClientMessageID: "provider-kind-interruption", ReasonCode: "model_provider_unavailable",
				ProviderFailureKind: kind, ResumeDetail: "original private evidence", Cause: &cause, Resumable: true, AutoResume: false,
			}
			for _, bad := range []string{"PRIVATE_KIND", " subscription", "subscription "} {
				invalid := input
				invalid.ProviderFailureKind = bad
				if _, err := repo.InterruptRunner(ctx, invalid); err == nil {
					t.Fatal("unknown provider category was accepted")
				}
			}
			invalid := input
			invalid.ReasonCode = "provider_stream_interrupted"
			if _, err := repo.InterruptRunner(ctx, invalid); err == nil {
				t.Fatal("provider category was attached to a different durable reason")
			}
			if _, found, err := repo.LatestResumableCheckpoint(ctx, streamID, "owner-a"); err != nil || found {
				t.Fatalf("invalid category partially persisted: found=%t err=%v", found, err)
			}
			result, err := repo.InterruptRunner(ctx, input)
			if err != nil || !result.Created || !result.Checkpoint.Resumable {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			var payload map[string]any
			if err := json.Unmarshal(result.Event.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			actualCause, present, err := ParseRunnerInterruptionCause(payload)
			if err != nil || !present || actualCause != cause || payload["provider_failure_kind"] != kind || payload["auto_resume"] != false {
				t.Fatalf("typed interruption lost category/cause: payload=%#v err=%v", payload, err)
			}
			reopenedDB, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer reopenedDB.Close()
			reopened := NewRepository(reopenedDB)
			got, found, err := reopened.LatestRunnerInterruption(ctx, streamID, "owner-a")
			if err != nil || !found || got.ProviderFailureKind != kind || got.AutoResume || got.ResumeDetail != input.ResumeDetail || got.EventID != result.Event.EventID {
				t.Fatalf("reopened=%#v found=%t err=%v", got, found, err)
			}
			changed := input
			changed.ProviderFailureKind = "authentication"
			if kind == "authentication" {
				changed.ProviderFailureKind = "subscription"
			}
			if _, err := repo.InterruptRunner(ctx, changed); err == nil {
				t.Fatal("same checkpoint identity overwrote its category")
			}
		})
	}
}
