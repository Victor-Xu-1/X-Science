package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

const RunnerTimingResumeEventType = "runner_timing_resumed"
const runnerTimingResumeSchema = "x-science.runner_timing_resume.v1"
const runnerTimingResumeClientPrefix = "runner-timing-resume:"

// This records the missing lease-generation boundary, not a second clock or
// execution authority. Only the successful claim transaction can append it.
type runnerTimingResumeFact struct {
	Schema                string    `json:"schema"`
	OwnerID               string    `json:"owner_id"`
	StreamUID             string    `json:"stream_uid"`
	Attempt               int64     `json:"attempt"`
	PreviousGeneration    string    `json:"previous_generation"`
	Generation            string    `json:"generation"`
	PreviousInputRevision int64     `json:"previous_input_revision"`
	InputRevision         int64     `json:"input_revision"`
	PreviousExpiresAt     time.Time `json:"previous_expires_at"`
	ResumedAt             time.Time `json:"resumed_at"`
	LegacyPrefixUnknown   bool      `json:"legacy_prefix_unknown,omitempty"`
}

func runnerTimingGeneration(ownerID, streamUID string, attempt int64, claimDigest []byte) string {
	raw := []byte("x-science.runner_timing_generation.v1\x00" + ownerID + "\x00" + streamUID + "\x00" + strconv.FormatInt(attempt, 10) + "\x00")
	digest := sha256.Sum256(append(raw, claimDigest...))
	return hex.EncodeToString(digest[:])
}

func prepareRunnerTimingResumeFactConn(ctx context.Context, conn *sql.Conn, stream Stream, attempt, revision int64, newDigest []byte, now time.Time) (runnerTimingResumeFact, error) {
	var priorDigest []byte
	var priorSource string
	var priorCheckpointAttempt int64
	fact := runnerTimingResumeFact{Schema: runnerTimingResumeSchema, OwnerID: stream.OwnerID, StreamUID: stream.UID, Attempt: attempt, InputRevision: revision, ResumedAt: now}
	err := conn.QueryRowContext(ctx, `
		SELECT claim_token_sha256,claimed_input_revision,expires_at,resume_source,
			COALESCE((SELECT runner_attempt FROM transcript_runner_checkpoints checkpoint
				WHERE checkpoint.stream_uid=attempt.stream_uid AND checkpoint.checkpoint_sequence=attempt.resume_checkpoint_sequence),0)
		FROM transcript_runner_attempts attempt WHERE stream_uid=? AND attempt=?`, stream.UID, attempt).
		Scan(&priorDigest, &fact.PreviousInputRevision, &fact.PreviousExpiresAt, &priorSource, &priorCheckpointAttempt)
	if err != nil {
		return runnerTimingResumeFact{}, err
	}
	fact.PreviousExpiresAt = fact.PreviousExpiresAt.UTC()
	fact.PreviousGeneration = runnerTimingGeneration(stream.OwnerID, stream.UID, attempt, priorDigest)
	fact.Generation = runnerTimingGeneration(stream.OwnerID, stream.UID, attempt, newDigest)
	// Relevant only for the first retained fact. Later complete generation
	// links retain the initial prefix classification; a missing link is unknown.
	fact.LegacyPrefixUnknown = priorSource == string(ResumeSourceCheckpoint) && priorCheckpointAttempt == attempt
	return fact, nil
}

func appendRunnerTimingResumeFactConn(ctx context.Context, conn *sql.Conn, stream Stream, fact runnerTimingResumeFact) error {
	payload, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	_, created, err := appendEventConn(ctx, conn, stream, eventRecord{
		clientMessageID: runnerTimingResumeClientPrefix + fact.Generation,
		eventType:       RunnerTimingResumeEventType, source: EventSourcePayload,
		runnerAttempt: &fact.Attempt, payloadJSON: payload, createdAt: fact.ResumedAt,
	})
	if err == nil && !created {
		return ErrEventConflict
	}
	return err
}

// IsRunnerInternalFact is shared by public cursor consumers. Timing facts are
// reserved even if damaged: an invalid internal payload must not become public
// activity. Their clock reader separately verifies the complete typed identity.
func IsRunnerInternalFact(event Event, payload []byte) bool {
	return event.Type == RunnerTimingResumeEventType || IsRunnerAdmissionFact(event, payload)
}
