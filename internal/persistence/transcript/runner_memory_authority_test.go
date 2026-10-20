package transcript

import (
	"testing"
	"time"
)

func TestClaimedMemoryContextValidatesAuthority(t *testing.T) {
	for _, scenario := range []string{"canonical", "legacy", "missing-authority", "mismatched-pair", "contradictory-receipts", "missing-receipt", "different-stream", "different-epoch"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db, stream, claim := claimedComposerFixture(t, false)
			switch scenario {
			case "legacy":
				forceLegacyFrameAuthorityForTest(t, db, stream)
			case "missing-authority":
				if _, err := db.Exec(`DELETE FROM transcript_frame_authority WHERE active_stream_uid=?`, stream.UID); err != nil {
					t.Fatal(err)
				}
			case "mismatched-pair":
				if _, err := db.Exec(`UPDATE transcript_frame_authority SET write_authority='legacy_frame_ref_v1' WHERE active_stream_uid=?`, stream.UID); err == nil {
					t.Fatal("authority integrity allowed mismatched pair")
				}
			case "contradictory-receipts":
				if _, err := db.Exec(`UPDATE transcript_frame_authority SET activation_id=zeroblob(32),genesis_id=zeroblob(32) WHERE active_stream_uid=?`, stream.UID); err == nil {
					t.Fatal("authority integrity allowed contradictory receipts")
				}
			case "missing-receipt":
				if _, err := db.Exec(`UPDATE transcript_frame_authority SET activation_id=NULL,genesis_id=NULL WHERE active_stream_uid=?`, stream.UID); err == nil {
					t.Fatal("authority integrity allowed missing payload receipt")
				}
			case "different-stream":
				if _, err := db.Exec(`UPDATE transcript_frame_authority SET active_stream_uid='other' WHERE active_stream_uid=?`, stream.UID); err == nil {
					t.Fatal("authority integrity allowed foreign stream")
				}
			case "different-epoch":
				if _, err := db.Exec(`UPDATE transcript_frame_authority SET active_epoch=active_epoch+1 WHERE active_stream_uid=?`, stream.UID); err == nil {
					t.Fatal("authority integrity allowed mismatched epoch")
				}
			}
			context, err := repo.ClaimedRunnerMemoryContext(t.Context(), claim)
			if scenario == "canonical" {
				if err != nil || !context.Canonical || context.Explicit {
					t.Fatalf("canonical context=%+v err=%v", context, err)
				}
			} else if scenario == "legacy" {
				if err != nil || context.Canonical || context.Explicit {
					t.Fatalf("legacy context=%+v err=%v", context, err)
				}
			} else if scenario == "missing-authority" {
				if err == nil {
					t.Fatalf("missing authority treated as legacy: %+v", context)
				}
			} else if err != nil || !context.Canonical {
				t.Fatalf("rejected corruption changed authority: %+v %v", context, err)
			}
		})
	}
}

func TestClaimedMemoryContextHonorsInputDataMemory(t *testing.T) {
	for _, test := range []struct {
		name    string
		data    map[string]any
		runtime map[string]any
		want    string
	}{
		{name: "input only off", data: map[string]any{"memory_mode": "off"}, want: "off"},
		{name: "camel input off", data: map[string]any{"memoryMode": "off"}, want: "off"},
		{name: "input off overrides runtime on", data: map[string]any{"memory_mode": "off"}, runtime: map[string]any{"memoryMode": "on"}, want: "off"},
		{name: "input on overrides runtime off", data: map[string]any{"memoryMode": "on"}, runtime: map[string]any{"memory_mode": "off"}, want: "on"},
		{name: "input snake precedence", data: map[string]any{"memory_mode": "off", "memoryMode": "on"}, want: "off"},
		{name: "invalid present", data: map[string]any{"memory_mode": true}},
		{name: "null present", data: map[string]any{"memory_mode": nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, _, stream := memoryAdmissionFixture(t, `{}`)
			input := memoryAdmissionInput(stream, "prior")
			input.RuntimeConfig = map[string]any{"memoryMode": "on"}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			input = memoryAdmissionInput(stream, "current")
			input.InputData, input.RuntimeConfig = test.data, test.runtime
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "input", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
			context, err := repo.ClaimedRunnerMemoryContext(t.Context(), claim.Claim)
			if test.want == "" {
				if err == nil {
					t.Fatalf("invalid input inherited on: %+v", context)
				}
			} else if err != nil || !context.Canonical || !context.Explicit || context.Mode != test.want {
				t.Fatalf("input context=%+v err=%v", context, err)
			}
		})
	}
}
