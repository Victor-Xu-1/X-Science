package transcript

import (
	"context"
	"errors"
	"testing"
	"time"
)

func expireAdmissionClaim(t *testing.T, repo *Repository, claim RunnerClaim) {
	t.Helper()
	if _, err := repo.db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`,
		time.Now().UTC().Add(-time.Minute), claim.StreamUID, claim.Attempt); err != nil {
		t.Fatal(err)
	}
}

func removePreparationProof(t *testing.T, repo *Repository, claim RunnerClaim) {
	t.Helper()
	// Reconstruct the legacy absence of typed admission without deleting event
	// identities or the active branch's membership.
	if _, err := repo.db.Exec(`UPDATE transcript_events SET event_type='legacy_claim_note'
		WHERE stream_uid=? AND runner_attempt=? AND event_type='runner_preparation_admitted'`, claim.StreamUID, claim.Attempt); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyCheckpointlessAdmissionIsNotReplayed(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "legacy-unknown-admission", "owner-a")
	removePreparationProof(t, repo, claim)
	expireAdmissionClaim(t, repo, claim)
	next, err := repo.ClaimNextRunner(t.Context(), ClaimNextRunnerInput{RunnerID: "fresh-worker", TTL: time.Minute})
	if err != nil || next.Claimed {
		t.Fatalf("unknown prior dispatch was replayed: next=%#v error=%v", next, err)
	}
	candidates, err := repo.ListAllExpiredUnrecoverableRunnerCandidates(t.Context(), 10)
	if err != nil || len(candidates) != 1 || candidates[0].CheckpointSequence != 0 {
		t.Fatalf("ambiguous admission was stranded: candidates=%#v error=%v", candidates, err)
	}
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(t.Context(), candidates[0])
	if err != nil || !applied {
		t.Fatalf("unknown-outcome settlement=%t error=%v", applied, err)
	}
}

func TestDirectFreshClaimRejectsAmbiguousCheckpointlessAdmission(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "direct-unknown-admission", "owner-a")
	removePreparationProof(t, repo, claim)
	expireAdmissionClaim(t, repo, claim)
	next, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: claim.StreamUID,
		OwnerID: claim.OwnerID, RunnerID: "direct-worker", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || next.Claimed {
		t.Fatalf("direct fresh claim replayed unknown dispatch: %#v error=%v", next, err)
	}
}

func TestInternalRunnerAdmissionCannotBecomeGenericFreshWork(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	started, err := repo.StartInternalFrameRunner(t.Context(), seedInternalAdmissionInput(t, repo))
	if err != nil || !started.Claimed {
		t.Fatalf("start=%#v error=%v", started, err)
	}
	state, err := repo.GetRunnerRuntimeState(t.Context(), started.Claim.StreamUID, started.Claim.OwnerID, started.Claim.Attempt)
	if err != nil || state.LastCheckpointSequence == 0 || state.Phase != RunnerPhaseExecuting {
		t.Fatalf("execution handoff lacks durable admission: state=%#v error=%v", state, err)
	}
	expireAdmissionClaim(t, repo, started.Claim)
	next, err := repo.ClaimNextRunner(t.Context(), ClaimNextRunnerInput{RunnerID: "generic-worker", TTL: time.Minute})
	if err != nil || next.Claimed {
		t.Fatalf("accepted execution became generic work: next=%#v error=%v", next, err)
	}
	replay, err := repo.ListRunnerReplay(t.Context(), ListRunnerReplayInput{
		StreamUID: started.Claim.StreamUID, OwnerID: started.Claim.OwnerID, MessageLimit: 10, CheckpointLimit: 10})
	if err != nil || len(replay) != 1 || replay[0].Event.Type != "user_message" {
		t.Fatalf("admission leaked into provider history: replay=%#v error=%v", replay, err)
	}
	var deliveries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_delivery_intents intent JOIN transcript_events event
		ON event.stream_uid=intent.stream_uid AND event.publication_seq=intent.publication_seq
		WHERE event.stream_uid=? AND event.event_type!='user_message'`, started.Claim.StreamUID).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("admission manufactured user-facing delivery: count=%d error=%v", deliveries, err)
	}
}

func seedInternalAdmissionInput(t *testing.T, repo *Repository) StartInternalFrameRunnerInput {
	t.Helper()
	if _, err := repo.db.Exec(`INSERT INTO projects(id,user_id) VALUES('project-a','owner-a');
		INSERT INTO frames(id,project_id,root_frame_id) VALUES('frame-a','project-a','root-a')`); err != nil {
		t.Fatal(err)
	}
	return StartInternalFrameRunnerInput{
		Stream: CreateStreamInput{UID: "internal-admission", OwnerID: "owner-a", ExternalID: "frame-a",
			SessionID: "frame-a", Kind: StreamKindFrameRef, ProjectID: "project-a", RootFrameID: "root-a", FrameID: "frame-a", Epoch: 1},
		UserEvent: AppendUserEventInput{StreamUID: "internal-admission", OwnerID: "owner-a",
			ClientMessageID: "internal-input", PayloadJSON: []byte(`{"text":"accepted internal work"}`)},
		RunnerID: "internal-executor", TTL: time.Minute,
	}
}

func TestInternalExecutionAdmissionFailureRollsBackHandoff(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	input := seedInternalAdmissionInput(t, repo)
	if _, err := db.Exec(`CREATE TRIGGER reject_execution_admission BEFORE INSERT ON transcript_runner_checkpoints
		BEGIN SELECT RAISE(ABORT,'execution admission unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.StartInternalFrameRunner(t.Context(), input); err == nil {
		t.Fatal("execution authority escaped without durable admission")
	}
	var streams int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_streams WHERE stream_uid=?`, input.Stream.UID).Scan(&streams); err != nil || streams != 0 {
		t.Fatalf("partial internal admission survived: streams=%d error=%v", streams, err)
	}
}

func TestPreparationAdmissionCannotBeForgedByUntypedEvent(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "admission-forgery", "owner-a")
	_, _, err := repo.AppendRunnerEvent(context.Background(), AppendEventInput{Claim: claim,
		ClientMessageID: "forged-admission", Type: "runner_preparation_admitted", Source: EventSourcePayload,
		PayloadJSON: []byte(`{"dispatch_state":"preparation","claimed_input_revision":1}`)})
	if !errors.Is(err, ErrReservedEventType) {
		t.Fatalf("untyped event forged preparation authority: %v", err)
	}
}

func TestPreparationProofCannotOverrideAdvancedOrWaitingPhase(t *testing.T) {
	for _, phase := range []RunnerPhase{RunnerPhaseExecuting, RunnerPhaseWaitingUser, RunnerPhaseWaitingApproval, RunnerPhaseWaitingExternal} {
		t.Run(string(phase), func(t *testing.T) {
			repo, db, _ := newTranscriptRepository(t)
			claim := seedArtifactProjectionClaim(t, repo, db, "admission-phase", "owner-a")
			if _, err := db.Exec(`UPDATE transcript_runner_attempts SET phase=? WHERE stream_uid=?`, string(phase), claim.StreamUID); err != nil {
				t.Fatal(err)
			}
			expireAdmissionClaim(t, repo, claim)
			next, err := repo.ClaimNextRunner(t.Context(), ClaimNextRunnerInput{RunnerID: "phase-worker", TTL: time.Minute})
			if err != nil || next.Claimed {
				t.Fatalf("advanced phase was replayed: %#v error=%v", next, err)
			}
			candidates, err := repo.ListAllExpiredUnrecoverableRunnerCandidates(t.Context(), 10)
			want := 0
			if phase == RunnerPhaseExecuting {
				want = 1
			}
			if err != nil || len(candidates) != want {
				t.Fatalf("phase=%s candidates=%#v error=%v", phase, candidates, err)
			}
			if phase != RunnerPhaseExecuting {
				applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(t.Context(), AutoResumeCandidate{
					StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, FrameID: "frame-a", Attempt: claim.Attempt})
				if err != nil || applied {
					t.Fatalf("deliberate wait was failed: applied=%t error=%v", applied, err)
				}
			}
		})
	}
}

func TestPreparationProofRequiresMatchingInputRevision(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "admission-revision", "owner-a")
	if _, err := db.Exec(`UPDATE transcript_events SET payload_json=json_set(payload_json,'$.claimed_input_revision',99)
		WHERE stream_uid=? AND event_type=?`, claim.StreamUID, RunnerPreparationAdmissionEventType); err != nil {
		t.Fatal(err)
	}
	expireAdmissionClaim(t, repo, claim)
	next, err := repo.ClaimNextRunner(t.Context(), ClaimNextRunnerInput{RunnerID: "wrong-revision", TTL: time.Minute})
	if err != nil || next.Claimed {
		t.Fatalf("mismatched admission proof was accepted: %#v error=%v", next, err)
	}
}

func TestCheckpointlessUnknownSettlementRejectsNewerInputAndCheckpoint(t *testing.T) {
	for _, newer := range []string{"input", "checkpoint"} {
		t.Run(newer, func(t *testing.T) {
			repo, db, _ := newTranscriptRepository(t)
			claim := seedArtifactProjectionClaim(t, repo, db, "legacy-newer", "owner-a")
			removePreparationProof(t, repo, claim)
			candidate := AutoResumeCandidate{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, FrameID: "frame-a", Attempt: claim.Attempt}
			if newer == "input" {
				if _, _, err := repo.AppendUserEvent(t.Context(), AppendUserEventInput{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID,
					ClientMessageID: "new-input", PayloadJSON: []byte(`{"text":"new task instruction"}`)}); err != nil {
					t.Fatal(err)
				}
			} else if _, _, _, err := repo.AppendRunnerCheckpoint(t.Context(), AppendRunnerCheckpointInput{
				Claim: claim, ClientMessageID: "new-checkpoint", Phase: RunnerPhasePlanning, Resumable: true, PayloadJSON: []byte(`{"status":"running"}`)}); err != nil {
				t.Fatal(err)
			}
			expireAdmissionClaim(t, repo, claim)
			applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(t.Context(), candidate)
			if err != nil || applied {
				t.Fatalf("stale zero-checkpoint candidate settled newer %s: applied=%t error=%v", newer, applied, err)
			}
		})
	}
}

func TestPreparationAdmissionFailureRollsBackClaim(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	seedTranscriptInput(t, repo, "admission-rollback", "owner-a")
	if _, err := db.Exec(`CREATE TRIGGER reject_preparation BEFORE INSERT ON transcript_events
		WHEN NEW.event_type='runner_preparation_admitted' BEGIN SELECT RAISE(ABORT,'admission unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: "admission-rollback", OwnerID: "owner-a",
		RunnerID: "rollback-worker", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err == nil {
		t.Fatal("claim returned without a durable preparation admission")
	}
	var attempts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_attempts WHERE stream_uid='admission-rollback'`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("partial claim survived rollback: attempts=%d error=%v", attempts, err)
	}
}
