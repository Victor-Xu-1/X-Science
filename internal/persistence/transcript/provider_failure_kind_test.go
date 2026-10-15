package transcript

import (
	"bytes"
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

func TestLatestRunnerInterruptionIgnoresOptionalProviderPresentationDrift(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		value        any
		autoResume   bool
	}{
		{"unknown", "model_provider_unavailable", "future_provider_category", false},
		{"non_string", "model_provider_unavailable", 7, false},
		{"empty", "model_provider_unavailable", "", false},
		{"other_reason", "provider_stream_no_progress", "subscription", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo, db, dsn := newTranscriptRepository(t)
			seedTranscriptInput(t, repo, "optional-provider-kind", "owner-a")
			claim, err := repo.ClaimRunner(ctx, ClaimRunnerInput{
				StreamUID: "optional-provider-kind", OwnerID: "owner-a", RunnerID: "optional-provider-kind-runner",
				TTL: time.Minute, ResumeSource: ResumeSourceFresh,
			})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%#v err=%v", claim, err)
			}
			cause := RunnerInterruptionCause{ReasonCode: test.reason, Detail: "exact original host cause"}
			interrupted, err := repo.InterruptRunner(ctx, InterruptRunnerInput{
				Claim: claim.Claim, ClientMessageID: "optional-provider-kind-checkpoint", ReasonCode: test.reason,
				ResumeDetail: "original private diagnostic", Cause: &cause, Resumable: true, AutoResume: test.autoResume,
			})
			if err != nil || !interrupted.Created {
				t.Fatalf("interruption=%#v err=%v", interrupted, err)
			}
			// Reconstruct a historical/future annotation in this private fixture.
			// All original execution facts and cause bytes remain unchanged.
			var payload map[string]any
			if err := json.Unmarshal(interrupted.Event.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			payload["provider_failure_kind"] = test.value
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND event_id=?`, raw, claim.Claim.StreamUID, interrupted.Event.EventID); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopenedDB, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer reopenedDB.Close()
			reopened := NewRepository(reopenedDB)
			got, found, err := reopened.LatestRunnerInterruption(ctx, claim.Claim.StreamUID, "owner-a")
			if err != nil || !found {
				t.Errorf("optional display data blocked the valid interruption: found=%t err=%v", found, err)
			} else if got.ProviderFailureKind != "" || got.ReasonCode != test.reason || got.AutoResume != test.autoResume ||
				got.ResumeDetail != "original private diagnostic" || got.EventID != interrupted.Event.EventID || got.CheckpointSequence != interrupted.Checkpoint.Sequence {
				t.Errorf("reader changed execution authority while ignoring display data: %#v", got)
			}
			var stored []byte
			if err := reopenedDB.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, claim.Claim.StreamUID, interrupted.Event.EventID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stored, raw) {
				t.Fatal("reading optional presentation data rewrote private event bytes")
			}
			var persisted map[string]any
			if err := json.Unmarshal(stored, &persisted); err != nil {
				t.Fatal(err)
			}
			gotCause, present, err := ParseRunnerInterruptionCause(persisted)
			if err != nil || !present || gotCause != cause {
				t.Fatalf("the original cause changed: present=%t cause=%#v err=%v", present, gotCause, err)
			}
		})
	}
}
