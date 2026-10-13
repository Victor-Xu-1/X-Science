package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"strings"
	"testing"
	"time"
)

const testProviderRetryReason = "model_provider_temporarily_unavailable"

func interruptProviderRetryFixture(t *testing.T, repo *Repository, claim RunnerClaim, autoResume bool) InterruptRunnerResult {
	t.Helper()
	result, err := repo.InterruptRunner(context.Background(), InterruptRunnerInput{
		Claim: claim, ClientMessageID: "provider-unavailable", ReasonCode: testProviderRetryReason,
		ResumeDetail: "sanitized provider condition", AutoResume: autoResume, Resumable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resumeProviderRetryFixture(t *testing.T, repo *Repository, claim RunnerClaim, interrupted InterruptRunnerResult) RunnerClaim {
	t.Helper()
	result, err := repo.ClaimRunner(context.Background(), ClaimRunnerInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, RunnerID: "retry-runner", TTL: time.Minute,
		ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !result.Claimed {
		t.Fatalf("resume=%#v err=%v", result, err)
	}
	return result.Claim
}

func TestRunnerAutomaticRetryReasonPersistsWithoutPublicProgress(t *testing.T) {
	repo, db, dsn := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "retry-projection", "owner-a")
	interrupted := interruptProviderRetryFixture(t, repo, claim, true)
	assertReason := func(repository *Repository, expected string) {
		t.Helper()
		reason, err := repository.RunnerAutomaticRetryReason(context.Background(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision, claim.Attempt)
		if err != nil || reason != expected {
			t.Fatalf("reason=%q want=%q err=%v", reason, expected, err)
		}
	}
	assertReason(repo, testProviderRetryReason)
	claim = resumeProviderRetryFixture(t, repo, claim, interrupted)
	if _, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "retry-start", Phase: RunnerPhasePlanning, Resumable: true,
		PayloadJSON: []byte(`{"status":"running","detail":"runner chat started"}`),
	}); err != nil {
		t.Fatal(err)
	}
	for index, payload := range []string{
		`{"text":""}`, `{"text":"private activity","block_type":"thinking"}`,
		`{"text":"unpublished draft","visibility":"provisional"}`,
		`{"text":"unpublished candidate","visibility":"provisional"}`,
	} {
		if _, _, err := repo.AppendRunnerEvent(context.Background(), AppendEventInput{
			Claim: claim, ClientMessageID: []string{"empty", "private", "provisional-delta", "provisional-message"}[index],
			Type:   []string{"content_delta", "content_delta", "content_delta", "assistant_message"}[index],
			Source: EventSourcePayload, PayloadJSON: []byte(payload),
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertReason(repo, testProviderRetryReason)
	reopened, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertReason(NewRepository(reopened), testProviderRetryReason)
	if reason, err := repo.RunnerAutomaticRetryReason(context.Background(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision, 0); err != nil || reason != "" {
		t.Fatalf("unclaimed timing reason=%q err=%v", reason, err)
	}
	if _, err := repo.RunnerAutomaticRetryReason(context.Background(), claim.StreamUID, "foreign-owner", claim.ClaimedInputRevision, claim.Attempt); err != ErrOwnerMismatch {
		t.Fatalf("foreign owner err=%v", err)
	}
	if reason, err := repo.RunnerAutomaticRetryReason(context.Background(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision, claim.Attempt+1); err != nil || reason != "" {
		t.Fatalf("stale attempt reason=%q err=%v", reason, err)
	}
	if _, _, err := repo.AppendUserEvent(context.Background(), AppendUserEventInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ClientMessageID: "new-input", PayloadJSON: []byte(`{"text":"new task input"}`),
	}); err != nil {
		t.Fatal(err)
	}
	assertReason(repo, "")
}

func TestRunnerAutomaticRetryProjectionUsesExistingIndexes(t *testing.T) {
	_, db, _ := newTranscriptRepository(t)
	for _, query := range []struct {
		name, sql string
		args      []any
	}{
		{"checkpoint", runnerRetryCheckpointQuery, []any{"stream", 1, 1}},
		{"progress", runnerRetryProgressQuery, []any{"stream", 10, 1, 1}},
	} {
		t.Run(query.name, func(t *testing.T) {
			rows, err := db.Query("EXPLAIN QUERY PLAN "+query.sql, query.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Log(detail)
				if strings.Contains(detail, "USE TEMP B-TREE") || strings.Contains(detail, "SCAN event") || strings.Contains(detail, "SCAN checkpoint") {
					t.Fatalf("retry read model must use existing stream indexes: %s", detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunnerAutomaticRetryIgnoresLateOlderAttemptObservation(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	first := seedArtifactProjectionClaim(t, repo, db, "retry-attempt-fence", "owner-a")
	interruptProviderRetryFixture(t, repo, first, true)
	// Historical reclaim can leave two attempts for the same logical input.
	// Seed that supported persisted shape without changing scheduler policy.
	now := time.Now().UTC()
	if err := repo.withImmediate(context.Background(), func(conn *sql.Conn) error {
		stream, err := getStreamConn(context.Background(), conn, first.StreamUID, first.OwnerID)
		if err != nil {
			return err
		}
		// Use the canonical reclaim operation so the terminal phase, immutable
		// reclaimed event, finish coordinates and receipt agree with the schema.
		_, err = reclaimExpiredRunnerConn(context.Background(), conn, stream, first.Attempt, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	token := "retry-projection-test-token"
	digest := sha256.Sum256([]byte(token))
	second := first
	second.Attempt++
	second.RunnerID, second.ClaimToken = "newer-runner", token
	second.ResumeSource, second.ResumeCheckpoint = ResumeSourceRetry, 0
	second.ClaimedAt, second.ExpiresAt = now, now.Add(time.Minute)
	if _, err := db.Exec(`INSERT INTO transcript_runner_attempts(
		stream_uid,attempt,runner_id,claim_token_sha256,claimed_input_revision,resume_source,
		resume_checkpoint_sequence,status,phase,phase_sequence,last_checkpoint_sequence,claimed_at,expires_at
	) VALUES(?,?,?,?,?,'retry',0,'running','claimed',1,0,?,?)`, second.StreamUID, second.Attempt, second.RunnerID, digest[:], second.ClaimedInputRevision, now, second.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if reason, err := repo.RunnerAutomaticRetryReason(context.Background(), first.StreamUID, first.OwnerID, first.ClaimedInputRevision, first.Attempt); err != nil || reason != "" {
		t.Fatalf("stale caller coordinates reason=%q err=%v", reason, err)
	}
	if _, err := repo.InterruptRunner(context.Background(), InterruptRunnerInput{
		Claim: second, ClientMessageID: "newer-retry", ReasonCode: testProviderRetryReason,
		ResumeDetail: "provider still unavailable", AutoResume: true, Resumable: true,
	}); err != nil {
		t.Fatal(err)
	}
	err := repo.withImmediate(context.Background(), func(conn *sql.Conn) error {
		stream, err := getStreamConn(context.Background(), conn, first.StreamUID, first.OwnerID)
		if err != nil {
			return err
		}
		_, _, err = appendEventConn(context.Background(), conn, stream, eventRecord{
			clientMessageID: "late-old-observation", eventType: ToolOperationObservationEventType,
			source: EventSourcePayload, runnerAttempt: &first.Attempt,
			payloadJSON: []byte(`{"toolCallId":"old-call","toolName":"Bash","status":"completed"}`), createdAt: now,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if reason, err := repo.RunnerAutomaticRetryReason(context.Background(), second.StreamUID, second.OwnerID, second.ClaimedInputRevision, second.Attempt); err != nil || reason != testProviderRetryReason {
		t.Fatalf("old observation cleared newer retry: reason=%q err=%v", reason, err)
	}
}

func TestRunnerAutomaticRetryReasonClearsAtRealBoundaries(t *testing.T) {
	for _, boundary := range []string{"public-text", "tool-start", "completed", "cancelled", "manual-wait"} {
		t.Run(boundary, func(t *testing.T) {
			repo, db, _ := newTranscriptRepository(t)
			claim := seedArtifactProjectionClaim(t, repo, db, "retry-boundary", "owner-a")
			interrupted := interruptProviderRetryFixture(t, repo, claim, true)
			claim = resumeProviderRetryFixture(t, repo, claim, interrupted)
			var err error
			switch boundary {
			case "public-text":
				_, _, err = repo.AppendRunnerEvent(context.Background(), AppendEventInput{
					Claim: claim, ClientMessageID: "real-public-text", Type: "content_delta", Source: EventSourcePayload,
					PayloadJSON: []byte(`{"text":"The first source is available."}`),
				})
			case "tool-start":
				_, _, _, err = repo.AppendRunnerCheckpoint(context.Background(), AppendRunnerCheckpointInput{
					Claim: claim, ClientMessageID: "real-tool-start", Phase: RunnerPhaseExecuting, Resumable: true,
					PayloadJSON: []byte(`{"toolCallId":"call-1","toolName":"Read","toolPhase":"start"}`),
				})
			case "completed", "cancelled":
				_, _, _, err = repo.FinishRunner(context.Background(), FinishRunnerInput{
					Claim: claim, ClientMessageID: "terminal", Status: boundary, PayloadJSON: []byte(`{"status":"` + boundary + `"}`),
				})
			case "manual-wait":
				_, err = repo.InterruptRunner(context.Background(), InterruptRunnerInput{
					Claim: claim, ClientMessageID: "manual-wait", ReasonCode: testProviderRetryReason,
					ResumeDetail: "wait for changed conditions", AutoResume: false, Resumable: true,
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			reason, err := repo.RunnerAutomaticRetryReason(context.Background(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision, claim.Attempt)
			if err != nil || reason != "" {
				t.Fatalf("boundary=%s reason=%q err=%v", boundary, reason, err)
			}
		})
	}
}
