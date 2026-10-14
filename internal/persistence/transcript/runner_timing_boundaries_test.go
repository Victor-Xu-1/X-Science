package transcript

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunnerTimingResumeFactsAreAtomicAndDoNotRecordRenewals(t *testing.T) {
	f := newRunnerTimingFixture(t)
	f.now = f.now.Add(90 * time.Second)
	checkpoint := f.pause(t)
	f.now = f.now.Add(24 * time.Hour)
	f.resume(t, checkpoint)
	if renewed, err := f.repo.HeartbeatRunner(t.Context(), HeartbeatRunnerInput{Claim: f.claim, TTL: time.Hour}); err != nil || !renewed.Renewed {
		t.Fatalf("renewal=%+v err=%v", renewed, err)
	}
	if duplicate, err := f.repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: f.claim.StreamUID, OwnerID: f.claim.OwnerID, RunnerID: "duplicate", TTL: time.Hour, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint}); err != nil || duplicate.Claimed {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	var count, revision, deliveries int
	var raw string
	if err := f.db.QueryRow(`SELECT COUNT(*),payload_json FROM transcript_events WHERE stream_uid=? AND event_type=?`, f.claim.StreamUID, RunnerTimingResumeEventType).Scan(&count, &raw); err != nil || count != 1 {
		t.Fatalf("facts=%d err=%v", count, err)
	}
	if err := f.db.QueryRow(`SELECT input_revision FROM transcript_streams WHERE stream_uid=?`, f.claim.StreamUID).Scan(&revision); err != nil || int64(revision) != f.claim.ClaimedInputRevision {
		t.Fatalf("internal fact manufactured user input: revision=%d err=%v", revision, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_delivery_intents intent JOIN transcript_events event ON event.stream_uid=intent.stream_uid AND event.publication_seq=intent.publication_seq WHERE event.stream_uid=? AND event.event_type=?`, f.claim.StreamUID, RunnerTimingResumeEventType).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("internal fact created delivery: %d %v", deliveries, err)
	}
	digest := sha256.Sum256([]byte(f.claim.ClaimToken))
	if strings.Contains(raw, f.claim.ClaimToken) || strings.Contains(raw, string(digest[:])) {
		t.Fatal("timing fact retained credential material")
	}
	if _, _, err := f.repo.AppendRunnerEvent(t.Context(), AppendEventInput{Claim: f.claim, ClientMessageID: "forged-timing", Type: RunnerTimingResumeEventType, Source: EventSourcePayload, PayloadJSON: []byte(`{}`)}); !errors.Is(err, ErrReservedEventType) {
		t.Fatalf("runner could forge a timing fact: %v", err)
	}
	replay, err := f.repo.ListRunnerReplay(t.Context(), ListRunnerReplayInput{StreamUID: f.claim.StreamUID, OwnerID: f.claim.OwnerID, MessageLimit: 10, CheckpointLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range replay {
		if entry.Event.Type == RunnerTimingResumeEventType {
			t.Fatal("timing fact entered provider context")
		}
	}
	f.now = f.now.Add(30 * time.Second)
	f.check(t, 120*time.Second, 120*time.Second, true)
}

func TestRunnerTimingFactFailureRollsBackClaimRotation(t *testing.T) {
	f := newRunnerTimingFixture(t)
	f.now = f.now.Add(90 * time.Second)
	checkpoint := f.pause(t)
	before, err := f.repo.GetRunnerRuntimeState(t.Context(), f.claim.StreamUID, f.claim.OwnerID, f.claim.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER timing_failure BEFORE INSERT ON transcript_events WHEN NEW.event_type='runner_timing_resumed' BEGIN SELECT RAISE(ABORT,'controlled timing failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(24 * time.Hour)
	if result, err := f.repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: f.claim.StreamUID, OwnerID: f.claim.OwnerID, RunnerID: "resume-rollback", TTL: time.Hour, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint}); err == nil || result.Claimed {
		t.Fatalf("failed fact committed a claim: %+v %v", result, err)
	}
	after, err := f.repo.GetRunnerRuntimeState(t.Context(), f.claim.StreamUID, f.claim.OwnerID, f.claim.Attempt)
	if err != nil || !after.ExpiresAt.Equal(before.ExpiresAt) || after.RunnerID != before.RunnerID {
		t.Fatalf("claim rotation survived rollback: before=%+v after=%+v err=%v", before, after, err)
	}
	f.check(t, 90*time.Second, 90*time.Second, false)
}

func TestRunnerTimingLegacyMissingAndDamagedGenerationsAreUnknown(t *testing.T) {
	for _, damage := range []string{"legacy missing fact", "different owner", "zero previous revision", "unrecorded generation"} {
		t.Run(damage, func(t *testing.T) {
			f := newRunnerTimingFixture(t)
			f.now = f.now.Add(90 * time.Second)
			checkpoint := f.pause(t)
			f.now = f.now.Add(24 * time.Hour)
			if damage == "legacy missing fact" {
				f.legacyResume(t, checkpoint)
			} else {
				f.resume(t, checkpoint)
			}
			switch damage {
			case "legacy missing fact":
			case "unrecorded generation":
				digest := sha256.Sum256([]byte("controlled-unrecorded-claim"))
				if _, err := f.db.Exec(`UPDATE transcript_runner_attempts SET claim_token_sha256=? WHERE stream_uid=?`, digest[:], f.claim.StreamUID); err != nil {
					t.Fatal(err)
				}
			default:
				var raw []byte
				if err := f.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_type=?`, f.claim.StreamUID, RunnerTimingResumeEventType).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var fact runnerTimingResumeFact
				if err := json.Unmarshal(raw, &fact); err != nil {
					t.Fatal(err)
				}
				if damage == "different owner" {
					fact.OwnerID = "another-owner"
				} else {
					fact.PreviousInputRevision = 0
				}
				raw, _ = json.Marshal(fact)
				if _, err := f.db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND event_type=?`, raw, f.claim.StreamUID, RunnerTimingResumeEventType); err != nil {
					t.Fatal(err)
				}
			}
			latest, task, found, err := f.repo.GetRunnerTimingSummaries(t.Context(), f.claim.StreamUID, f.claim.OwnerID)
			if err != nil || !found || !latest.ElapsedUnknown || !task.ElapsedUnknown {
				t.Fatalf("missing causal history invented precise duration: latest=%+v task=%+v err=%v", latest, task, err)
			}
		})
	}
}

func TestRunnerTimingUnknownHistoryDoesNotPoisonIndependentNewInputClock(t *testing.T) {
	f := newRunnerTimingFixture(t)
	f.now = f.now.Add(90 * time.Second)
	checkpoint := f.pause(t)
	f.now = f.now.Add(24 * time.Hour)
	f.legacyResume(t, checkpoint)
	f.pause(t)
	f.newInput(t)
	f.now = f.now.Add(30 * time.Second)
	latest, task, found, err := f.repo.GetRunnerTimingSummaries(t.Context(), f.claim.StreamUID, f.claim.OwnerID)
	if err != nil || !found || latest.ElapsedUnknown || latest.Elapsed != 30*time.Second || !task.ElapsedUnknown || !latest.ObservedAt.Equal(task.ObservedAt) || latest.SnapshotInputRevision != latest.InputRevision {
		t.Fatalf("new input/whole task uncertainty was merged: latest=%+v task=%+v err=%v", latest, task, err)
	}
}

// This disposable legacy fixture reproduces the pre-fix successful-resume row
// shape. It does not delete immutable branch membership or invent a historical
// fact that older binaries never recorded.
func (f *runnerTimingFixture) legacyResume(t *testing.T, checkpoint int64) {
	t.Helper()
	f.steps++
	token := "controlled-legacy-timing-token"
	digest := sha256.Sum256([]byte(token))
	expires := f.now.Add(time.Hour)
	if _, err := f.db.Exec(`UPDATE transcript_runner_attempts SET runner_id='legacy-resume',claim_token_sha256=?,resume_source='checkpoint',resume_checkpoint_sequence=?,expires_at=? WHERE stream_uid=? AND attempt=?`, digest[:], checkpoint, expires, f.claim.StreamUID, f.claim.Attempt); err != nil {
		t.Fatal(err)
	}
	f.claim.RunnerID, f.claim.ClaimToken = "legacy-resume", token
	f.claim.ResumeSource, f.claim.ResumeCheckpoint = ResumeSourceCheckpoint, checkpoint
	f.claim.ResumeCheckpointAttempt = f.claim.Attempt
	f.claim.ClaimedAt, f.claim.ExpiresAt = f.now, expires
}

func TestRunnerTimingLateInternalTerminalSettlementPreservesLeaseEnd(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }
	stream, claim := seedAskUserHistoryClassificationFrame(t, repo, db, "timing-internal-terminal", false)
	if _, err := repo.HeartbeatRunner(t.Context(), HeartbeatRunnerInput{Claim: claim, TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(90 * time.Second)
	if _, err := repo.InterruptRunner(t.Context(), InterruptRunnerInput{Claim: claim, ClientMessageID: "timing-internal-pause", ReasonCode: "runtime_draining", Resumable: true, AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if err := repo.RunImmediate(context.Background(), func(tx *ImmediateTransaction) error {
		_, _, _, err := tx.FinishLatestFrameRunner(t.Context(), stream.OwnerID, stream.FrameID, "cancelled", "cancelled after pause", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	latest, task, found, err := repo.GetRunnerTimingSummaries(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || !found || latest.Elapsed != 90*time.Second || task.Elapsed != 90*time.Second || latest.ElapsedUnknown || task.ElapsedUnknown || latest.FinishedAt == nil || !latest.FinishedAt.Equal(now) {
		t.Fatalf("terminal audit time became active work: latest=%+v task=%+v err=%v", latest, task, err)
	}
}

func TestRunnerTimingGenerationChainDoesNotBridgeAnUnrecordedResume(t *testing.T) {
	f := newRunnerTimingFixture(t)
	for range 2 {
		f.now = f.now.Add(20 * time.Second)
		checkpoint := f.pause(t)
		f.now = f.now.Add(24 * time.Hour)
		f.resume(t, checkpoint)
	}
	var eventID int64
	var raw []byte
	if err := f.db.QueryRow(`SELECT event_id,payload_json FROM transcript_events WHERE stream_uid=? AND event_type=? ORDER BY publication_seq DESC LIMIT 1`, f.claim.StreamUID, RunnerTimingResumeEventType).Scan(&eventID, &raw); err != nil {
		t.Fatal(err)
	}
	var fact runnerTimingResumeFact
	if err := json.Unmarshal(raw, &fact); err != nil {
		t.Fatal(err)
	}
	fact.PreviousGeneration = strings.Repeat("a", 64)
	raw, _ = json.Marshal(fact)
	if _, err := f.db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND event_id=?`, raw, f.claim.StreamUID, eventID); err != nil {
		t.Fatal(err)
	}
	latest, task, found, err := f.repo.GetRunnerTimingSummaries(t.Context(), f.claim.StreamUID, f.claim.OwnerID)
	if err != nil || !found || !latest.ElapsedUnknown || !task.ElapsedUnknown {
		t.Fatalf("missing generation link counted the unknown gap: latest=%+v task=%+v err=%v", latest, task, err)
	}
}

func TestRunnerTimingSameAttemptInputResponseRetainsPerIntervalRevision(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }
	if _, err := db.Exec(`INSERT INTO projects(id,user_id) VALUES('timing-response-project','owner-a'); INSERT INTO frames(id,project_id,root_frame_id) VALUES('timing-response-frame','timing-response-project','timing-response-frame')`); err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "frame:timing-response-frame", OwnerID: "owner-a", ExternalID: "timing-response-frame", SessionID: "timing-response-frame", Kind: StreamKindFrameRef, ProjectID: "timing-response-project", RootFrameID: "timing-response-frame", FrameID: "timing-response-frame", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "timing-response-input", FrameEventID: "timing-response-input-frame", MessageUUID: "timing-response-input", Text: "controlled timing task"}); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "timing-response-first", TTL: time.Hour, ResumeSource: ResumeSourceFresh})
	if err != nil || !first.Claimed {
		t.Fatalf("claim=%+v err=%v", first, err)
	}
	now = now.Add(90 * time.Second)
	paused, err := repo.InterruptRunner(t.Context(), InterruptRunnerInput{Claim: first.Claim, ClientMessageID: "timing-response-pause", ReasonCode: "runtime_draining", Resumable: true, AutoResume: true})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), AppendFrameInputResponseInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, FrameID: stream.FrameID, ClientMessageID: "timing-response", PayloadJSON: []byte(`{"role":"user","text":"resolved"}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "timing-response-second", TTL: time.Hour, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: paused.Checkpoint.Sequence})
	if err != nil || !resumed.Claimed || resumed.Claim.Attempt != first.Claim.Attempt || resumed.Claim.ClaimedInputRevision <= first.Claim.ClaimedInputRevision {
		t.Fatalf("same-attempt response=%+v err=%v", resumed, err)
	}
	now = now.Add(30 * time.Second)
	latest, task, found, err := repo.GetRunnerTimingSummaries(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || !found || latest.ElapsedUnknown || task.ElapsedUnknown || latest.Elapsed != 30*time.Second || task.Elapsed != 120*time.Second || !latest.StartedAt.Equal(resumed.Claim.ClaimedAt) || latest.SnapshotFrameStatus != "processing" {
		t.Fatalf("mutable attempt revision reassigned old intervals: latest=%+v task=%+v err=%v", latest, task, err)
	}
	if _, err := db.Exec(`UPDATE frames SET status='completed' WHERE id=?`, stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
		if _, _, err := tx.AppendFrameInputResponse(t.Context(), AppendFrameInputResponseInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, FrameID: stream.FrameID, ClientMessageID: "timing-pending-response", PayloadJSON: []byte(`{"role":"user","text":"next input"}`)}); err != nil {
			return err
		}
		_, err := tx.conn.ExecContext(t.Context(), `UPDATE frames SET status='processing' WHERE id=?`, stream.FrameID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	pending, _, found, err := repo.GetRunnerTimingSummaries(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || !found || pending.SnapshotInputRevision <= pending.InputRevision || pending.SnapshotFrameStatus != "processing" {
		t.Fatalf("pending input and frame status came from different snapshots: %+v err=%v", pending, err)
	}
}

func TestRunnerTimingPairedSnapshotAndIndexedFactRead(t *testing.T) {
	f := newRunnerTimingFixture(t)
	old, err := f.repo.GetStream(t.Context(), f.claim.StreamUID, f.claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(90 * time.Second)
	f.pause(t)
	if _, _, err := f.repo.AppendUserEvent(t.Context(), AppendUserEventInput{StreamUID: f.claim.StreamUID, OwnerID: f.claim.OwnerID, ClientMessageID: "timing-pending-input", PayloadJSON: []byte(`{"text":"a new round"}`)}); err != nil {
		t.Fatal(err)
	}
	latest, task, found, err := f.repo.GetRunnerTimingSummaries(t.Context(), f.claim.StreamUID, f.claim.OwnerID)
	if err != nil || !found || latest.SnapshotInputRevision <= latest.InputRevision || latest.SnapshotInputRevision <= old.InputRevision || !latest.SnapshotUpdatedAt.Equal(f.now) || !latest.ObservedAt.Equal(task.ObservedAt) {
		t.Fatalf("paired read used stale input metadata: latest=%+v task=%+v err=%v", latest, task, err)
	}
	rows, err := f.db.Query("EXPLAIN QUERY PLAN "+runnerTimingFactsQuery, f.claim.StreamUID, runnerTimingResumeClientPrefix, "runner-timing-resume;", RunnerTimingResumeEventType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "client_message_id>?") && strings.Contains(detail, "client_message_id<?")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("timing facts scan every task event instead of using the existing client-id range index")
	}
	if _, _, _, err := f.repo.GetRunnerTimingSummaries(t.Context(), f.claim.StreamUID, "wrong-owner"); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("paired timing read lost owner fence: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := f.repo.GetRunnerTimingSummaries(cancelled, f.claim.StreamUID, f.claim.OwnerID); !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation became clock-only unknown: %v", err)
	}
}

func TestRunnerTimingUnknownClockBoundaryDoesNotHideConversation(t *testing.T) {
	for _, boundary := range []string{"wall-clock rollback", "lease before claim", "unreadable timestamp"} {
		t.Run(boundary, func(t *testing.T) {
			f := newRunnerTimingFixture(t)
			if boundary == "wall-clock rollback" {
				f.now = f.now.Add(-time.Minute)
			} else if boundary == "unreadable timestamp" {
				if _, err := f.db.Exec(`UPDATE transcript_runner_attempts SET claimed_at='invalid-timing-value' WHERE stream_uid=?`, f.claim.StreamUID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=?`, f.now.Add(-time.Minute), f.claim.StreamUID); err != nil {
				t.Fatal(err)
			}
			latest, task, found, err := f.repo.GetRunnerTimingSummaries(t.Context(), f.claim.StreamUID, f.claim.OwnerID)
			if err != nil || !found || !latest.ElapsedUnknown || !task.ElapsedUnknown || !errors.Is(latest.ObservationError, ErrEventConflict) || !errors.Is(task.ObservationError, ErrEventConflict) || latest.Status != "running" {
				t.Fatalf("clock-only failure changed conversation authority: latest=%+v task=%+v err=%v", latest, task, err)
			}
			if _, err := f.repo.GetStream(t.Context(), f.claim.StreamUID, f.claim.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunnerTimingResumedClockRollbackRetainsTypedDiagnostic(t *testing.T) {
	startedAt := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	pausedAt := startedAt.Add(90 * time.Second)
	resumedAt := pausedAt.Add(24 * time.Hour)
	generation := strings.Repeat("a", 64)
	attempt := runnerTimingAttempt{
		attempt: 1, revision: 1, status: "running", generation: generation,
		claimedAt: startedAt, expiresAt: resumedAt.Add(time.Hour),
		facts: []runnerTimingFactRecord{{valid: true, fact: runnerTimingResumeFact{
			PreviousGeneration: strings.Repeat("b", 64), Generation: generation,
			PreviousInputRevision: 1, InputRevision: 1,
			PreviousExpiresAt: pausedAt, ResumedAt: resumedAt,
		}}},
	}
	latest, task, found, err := reduceRunnerTiming("controlled-stream", []runnerTimingAttempt{attempt}, resumedAt.Add(-time.Hour))
	if err != nil || !found || !latest.ElapsedUnknown || !task.ElapsedUnknown || !errors.Is(latest.ObservationError, ErrEventConflict) || !errors.Is(task.ObservationError, ErrEventConflict) || latest.Status != "running" {
		t.Fatalf("resumed clock rollback lost its nonfatal typed diagnostic: latest=%+v task=%+v err=%v", latest, task, err)
	}
}
