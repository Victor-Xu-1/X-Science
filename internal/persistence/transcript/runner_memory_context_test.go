package transcript

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClaimedRunnerMemoryModeUsesAdmittedRevision(t *testing.T) {
	repo, db, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"off"}}`)
	input := memoryAdmissionInput(stream, "first")
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "first", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if _, err := db.Exec(`UPDATE frame_runtime_metadata SET context_data='{"_original_input":{"memory_mode":"on"}}' WHERE frame_id='memory-root'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(stream, "queued")); err != nil {
		t.Fatal(err)
	}
	mode, found, err := repo.ClaimedRunnerMemoryMode(t.Context(), claim.Claim)
	if err != nil || !found || mode != "off" {
		t.Fatalf("queued input leaked: %s %t %v", mode, found, err)
	}
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "finish", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	next, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "next", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !next.Claimed {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	mode, found, err = repo.ClaimedRunnerMemoryMode(t.Context(), next.Claim)
	if err != nil || !found || mode != "on" {
		t.Fatalf("new claim missed capture: %s %t %v", mode, found, err)
	}
}

func TestClaimedRunnerMemoryModeRejectsStaleClaim(t *testing.T) {
	repo, _, _, claim := claimedComposerFixture(t, false)
	wrong := claim
	wrong.OwnerID = "other"
	if _, _, err := repo.ClaimedRunnerMemoryMode(t.Context(), wrong); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("wrong owner: %v", err)
	}
	wrong = claim
	wrong.ClaimToken = "wrong"
	if _, _, err := repo.ClaimedRunnerMemoryMode(t.Context(), wrong); !errors.Is(err, ErrClaimStale) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim, ClientMessageID: "finish", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ClaimedRunnerMemoryMode(t.Context(), claim); !errors.Is(err, ErrClaimStale) {
		t.Fatalf("terminal claim: %v", err)
	}
}

func TestClaimedRunnerMemoryModeInheritsOnlyProvenExplicitHistory(t *testing.T) {
	for _, value := range []any{"on", nil, true, "unknown"} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			repo, _, stream := memoryAdmissionFixture(t, `{}`)
			first := memoryAdmissionInput(stream, "first")
			if value != nil {
				first.RuntimeConfig = map[string]any{"memoryMode": value}
			}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), first); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(stream, "inherited")); err != nil {
				t.Fatal(err)
			}
			claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "inherit", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
			mode, found, err := repo.ClaimedRunnerMemoryMode(t.Context(), claim.Claim)
			if value == "on" && (err != nil || !found || mode != "on") {
				t.Fatalf("lost explicit history: %s %t %v", mode, found, err)
			}
			if value == nil && (err != nil || found || mode != "") {
				t.Fatalf("invented permission: %s %t %v", mode, found, err)
			}
			if value != nil && value != "on" && !errors.Is(err, ErrEventConflict) {
				t.Fatalf("invalid permission accepted: %s %t %v", mode, found, err)
			}
		})
	}
}

func TestClaimedRunnerMemoryModeRejectsChangedBranchGeneration(t *testing.T) {
	repo, db, stream, claim := claimedComposerFixture(t, false)
	if _, err := db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, stream.UID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ClaimedRunnerMemoryMode(t.Context(), claim); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("changed branch generation accepted: %v", err)
	}
}

func TestMemoryRuntimeConfigAliasesHonorLatestPatch(t *testing.T) {
	t.Run("captured off then explicit on", func(t *testing.T) {
		repo, _, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"off"}}`)
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(stream, "captured")); err != nil {
			t.Fatal(err)
		}
		input := memoryAdmissionInput(stream, "explicit")
		input.RuntimeConfig = map[string]any{"memoryMode": "on"}
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
			t.Fatal(err)
		}
		claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "explicit", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
		if err != nil || !claim.Claimed {
			t.Fatalf("claim=%+v err=%v", claim, err)
		}
		mode, found, err := repo.ClaimedRunnerMemoryMode(t.Context(), claim.Claim)
		if err != nil || !found || mode != "on" {
			t.Fatalf("captured old alias blocked new override: %s %t %v", mode, found, err)
		}
	})
	for _, firstKey := range []string{"memory_mode", "memoryMode"} {
		t.Run(firstKey, func(t *testing.T) {
			repo, _, stream := memoryAdmissionFixture(t, `{}`)
			input := memoryAdmissionInput(stream, "first")
			input.RuntimeConfig = map[string]any{firstKey: "off"}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			secondKey := "memoryMode"
			if firstKey == secondKey {
				secondKey = "memory_mode"
			}
			input = memoryAdmissionInput(stream, "next")
			input.RuntimeConfig = map[string]any{secondKey: "on"}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			config, found, err := repo.LatestFrameRuntimeConfig(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || !found || config[secondKey] != "on" || config[firstKey] != nil {
				t.Fatalf("aliases=%v found=%t err=%v", config, found, err)
			}
			input = memoryAdmissionInput(stream, "both")
			input.RuntimeConfig = map[string]any{"memory_mode": "off", "memoryMode": "on"}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "both", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
			mode, found, err := repo.ClaimedRunnerMemoryMode(t.Context(), claim.Claim)
			if err != nil || !found || mode != "off" {
				t.Fatalf("same-patch priority changed: %s %t %v", mode, found, err)
			}
		})
	}
}
