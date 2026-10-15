package workspace

import (
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestHistoricalStagedInputUsesExplicitContinueWithoutInventedAdmission(t *testing.T) {
	store, repo, stream := schema76QueuedInputUpgrade(t, true, false, legacyQueuedFixtureOptions{stagedBeforeClaim: true})
	evidence, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || evidence.Authorized || evidence.Coverage.Complete || !evidence.Coverage.Pending() {
		t.Fatalf("staged source guessed as fresh: %+v %v", evidence, err)
	}
	flag, err := repo.FramePendingInputRequiresContinue(t.Context(), stream.OwnerID, stream.FrameID)
	if err != nil || !flag {
		t.Fatalf("processing unknown hidden: %t %v", flag, err)
	}
	command := admissionContinueInput(t, store, stream)
	accepted, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, command)
	if err != nil || accepted.ContinueCommand == nil {
		t.Fatalf("processing continue: %+v %v", accepted, err)
	}
	again, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, command)
	if err != nil || again.Event.ID != accepted.Event.ID {
		t.Fatalf("continue receipt changed: %+v %v", again, err)
	}
	dispatch, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("historical-continue", time.Minute)
	if err != nil || !found {
		t.Fatalf("continue dispatch: %+v %v", dispatch, err)
	}
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput})
	if err != nil || !claim.Claimed || claim.Claim.ClaimedInputRevision != 2 {
		t.Fatalf("retained-prefix claim: %+v %v", claim, err)
	}
	var facts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=?`, stream.UID).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("invented history: %d %v", facts, err)
	}
}

func TestFreshAdmissionSupersedesUnmappedStandaloneInput(t *testing.T) {
	_, repo, stream := schema76QueuedInputUpgrade(t, false, false, legacyQueuedFixtureOptions{stagedBeforeClaim: true})
	before, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || before.Authorized || before.Coverage.Complete {
		t.Fatalf("unknown standalone evidence: %+v %v", before, err)
	}
	if _, created, err := repo.AppendUserEvent(t.Context(), transcriptstore.AppendUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		ClientMessageID: "new-explicit-input", PayloadJSON: []byte(`{"text":"Compare the retained evidence."}`)}); err != nil || !created {
		t.Fatalf("new ordinary input: %t %v", created, err)
	}
	after, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || !after.Authorized || after.Source != "exact_admission" || after.Coverage.Complete {
		t.Fatalf("new exact input behind gap: %+v %v", after, err)
	}
	claim, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "standalone-explicit", TTL: time.Minute})
	if err != nil || !claim.Claimed || claim.Claim.ClaimedInputRevision != 3 {
		t.Fatalf("standalone input stranded: %+v %v", claim, err)
	}
}

func TestLegacyFreshEvidencePreservesLaterManualWait(t *testing.T) {
	_, repo, stream := schema76QueuedInputUpgrade(t, true, true, legacyQueuedFixtureOptions{manualWait: true})
	evidence, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || evidence.Authorized || evidence.Coverage.Complete {
		t.Fatalf("legacy source overrode wait: %+v %v", evidence, err)
	}
	if claim, err := repo.ClaimNextFrameRunner(t.Context(), transcriptstore.ClaimNextFrameRunnerInput{RunnerID: "must-wait", TTL: time.Minute}); err != nil || claim.Claimed {
		t.Fatalf("manual wait stolen: %+v %v", claim, err)
	}
	if flag, err := repo.FramePendingInputRequiresContinue(t.Context(), stream.OwnerID, stream.FrameID); err != nil || !flag {
		t.Fatalf("manual continuation unavailable: %t %v", flag, err)
	}
}

func TestLegacyFreshEvidenceRejectsMismatchedPreparationAndConsumedPrefix(t *testing.T) {
	for _, boundary := range []string{"claim_revision", "consumed_prefix"} {
		t.Run(boundary, func(t *testing.T) {
			store, repo, stream := schema76QueuedInputUpgrade(t, true, true, legacyQueuedFixtureOptions{consumedControl: boundary == "consumed_prefix"})
			if boundary == "claim_revision" {
				// Controlled corruption tests the marker identity, not a runtime repair.
				if _, err := store.db.Exec(`UPDATE transcript_events SET payload_json=json_set(payload_json,'$.claimed_input_revision',99) WHERE stream_uid=? AND event_type='runner_preparation_admitted'`, stream.UID); err != nil {
					t.Fatal(err)
				}
			}
			evidence, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || evidence.Authorized || evidence.Coverage.Complete {
				t.Fatalf("invalid legacy boundary authorized: %+v %v", evidence, err)
			}
		})
	}
}
