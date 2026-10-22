package transcript

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const contextInspectionInputLimit = 128
const contextInspectionAttemptLimit = 2000

// ContextInspectionScope is a read coordinate, not execution authority. Every
// constituent is read from the same repository snapshot, never supplied by UI.
type ContextInspectionScope struct {
	ConversationID             string `json:"conversationId"`
	FrameIncarnationID         string `json:"frameIncarnationId"`
	StreamUID                  string `json:"streamUid"`
	StreamEpoch                int64  `json:"streamEpoch"`
	BranchID                   string `json:"branchId"`
	BranchGeneration           int64  `json:"branchGeneration"`
	ThroughPublicationSequence int64  `json:"throughPublicationSequence"`
}

// ContextCallAssociation captures an ordinary admitted prompt while its claim
// is live. Recovery/control inputs may advance ClaimedInputRevision without
// creating a new prompt. The original admission branch is retained for forks.
type ContextCallAssociation struct {
	Version                   int                    `json:"version"`
	Scope                     ContextInspectionScope `json:"scope"`
	InputMessageID            string                 `json:"inputMessageId"`
	InputEventID              int64                  `json:"inputEventId"`
	InputRevision             int64                  `json:"inputRevision"`
	InputPublicationSequence  int64                  `json:"inputPublicationSequence"`
	AdmissionBranchID         string                 `json:"admissionBranchId"`
	AdmissionBranchGeneration int64                  `json:"admissionBranchGeneration"`
	ClaimedInputRevision      int64                  `json:"claimedInputRevision"`
	RunnerAttempt             int64                  `json:"runnerAttempt"`
}

type ContextInspectionInput struct {
	InputMessageID                              string
	EventID, InputRevision, PublicationSequence int64
	AdmissionBranchID                           string
	AdmissionBranchGeneration                   int64
}

type ContextInspectionAttempt struct {
	Attempt, ClaimedInputRevision, InputEventID int64
	Status                                      string
	TerminalEventID                             int64
	CompletedAt                                 *time.Time
	ClaimedRevisions                            map[int64]bool
	generation                                  string
	resumeFacts                                 []runnerTimingFactRecord
}

type ContextInspectionSnapshot struct {
	Scope                 ContextInspectionScope
	Inputs                []ContextInspectionInput
	Attempts              []ContextInspectionAttempt
	AncestorBranches      map[string]bool
	Truncated             bool
	ConsumedInputRevision int64
	Outcomes              []ContextInspectionOutcome
	OutcomeIncomplete     bool
}

func contextInspectionScopeConn(ctx context.Context, conn *sql.Conn, stream Stream) (ContextInspectionScope, error) {
	_, _, projection, err := projectionSnapshotForBranchTx(ctx, conn, stream.UID, stream.OwnerID, "")
	if err != nil {
		return ContextInspectionScope{}, err
	}
	scope := ContextInspectionScope{ConversationID: stream.SessionID, StreamUID: stream.UID, StreamEpoch: stream.Epoch,
		BranchID: projection.BranchID, BranchGeneration: projection.BranchGeneration,
		ThroughPublicationSequence: projection.ThroughPublicationSequence}
	if stream.Kind != StreamKindFrameRef {
		return scope, nil
	}
	err = conn.QueryRowContext(ctx, `SELECT frame.incarnation_id FROM frames frame
		JOIN projects project ON project.id=frame.project_id WHERE frame.id=? AND project.user_id=?`, stream.FrameID, stream.OwnerID).Scan(&scope.FrameIncarnationID)
	return scope, err
}

// ReadContextInspection returns only ordinary admission and runner facts in the
// selected active branch. It reads no prompt text into the public projection.
func (r *Repository) ReadContextInspection(ctx context.Context, streamUID, ownerID string) (ContextInspectionSnapshot, error) {
	var result ContextInspectionSnapshot
	if r == nil || r.readDatabase() == nil {
		return result, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return result, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := getStreamConn(ctx, conn, streamUID, ownerID)
	if err != nil {
		return result, schemaError(err)
	}
	result.Scope, err = contextInspectionScopeConn(ctx, conn, stream)
	if err != nil {
		return result, schemaError(err)
	}
	result.ConsumedInputRevision = stream.ConsumedInputRevision
	result.Inputs, result.Truncated, err = contextInspectionInputsConn(ctx, conn, stream, result.Scope)
	if err != nil {
		return result, schemaError(err)
	}
	var truncated bool
	result.Attempts, truncated, err = contextInspectionAttemptsConn(ctx, conn, stream, result.Scope)
	result.Truncated = result.Truncated || truncated
	if err == nil {
		truncated, err = contextInspectionRevisionsConn(ctx, conn, stream, result.Attempts)
		result.Truncated = result.Truncated || truncated
	}
	if err == nil {
		result.AncestorBranches, truncated, err = contextInspectionAncestorsConn(ctx, conn, result.Scope)
		result.Truncated = result.Truncated || truncated
	}
	if err == nil {
		result.Outcomes, result.OutcomeIncomplete, truncated, err = contextInspectionOutcomesConn(ctx, conn, result.Scope, result.Attempts, result.Inputs)
		result.Truncated = result.Truncated || truncated
	}
	return result, schemaError(err)
}

// ValidateContextInspection is the final fence after reading operational usage
// from runtimekv. A branch, epoch, incarnation or publication change invalidates
// that composite read; callers retry instead of mixing two histories.
func (r *Repository) ValidateContextInspection(ctx context.Context, ownerID string, expected ContextInspectionScope) error {
	if r == nil || r.readDatabase() == nil {
		return ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := getStreamConn(ctx, conn, expected.StreamUID, ownerID)
	if err != nil {
		return err
	}
	current, err := contextInspectionScopeConn(ctx, conn, stream)
	if err == nil && current != expected {
		return ErrBranchStateStale
	}
	return schemaError(err)
}

func contextInspectionAttemptsConn(ctx context.Context, conn *sql.Conn, stream Stream, scope ContextInspectionScope) ([]ContextInspectionAttempt, bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT a.attempt,a.claimed_input_revision,a.status,COALESCE(a.finished_event_id,0),a.finished_at,
		COALESCE((SELECT input.event_id FROM transcript_input_admissions input
		 JOIN transcript_branch_events member ON member.stream_uid=input.stream_uid AND member.event_id=input.event_id AND member.branch_id=?
		 WHERE input.stream_uid=a.stream_uid AND input.input_kind='message' AND input.source_kind='event'
		 AND input.classification_version=? AND input.input_revision<=a.claimed_input_revision ORDER BY input.input_revision DESC LIMIT 1),0),
		COALESCE(event.event_type,''),COALESCE(event.runner_attempt,0),COALESCE(receipt.status,''),a.claim_token_sha256
		FROM transcript_runner_attempts a
		LEFT JOIN transcript_events event ON event.stream_uid=a.stream_uid AND event.event_id=a.finished_event_id
		LEFT JOIN transcript_runner_receipts receipt ON receipt.stream_uid=a.stream_uid AND receipt.attempt=a.attempt AND receipt.event_id=a.finished_event_id
		WHERE a.stream_uid=? AND EXISTS(SELECT 1 FROM transcript_branch_events member JOIN transcript_events fact
		 ON fact.stream_uid=member.stream_uid AND fact.event_id=member.event_id
		 WHERE member.stream_uid=a.stream_uid AND member.branch_id=? AND fact.runner_attempt=a.attempt AND fact.publication_seq<=?)
		AND (a.finished_event_id IS NULL OR EXISTS(SELECT 1 FROM transcript_branch_events terminal WHERE terminal.stream_uid=a.stream_uid AND terminal.branch_id=? AND terminal.event_id=a.finished_event_id))
		ORDER BY a.attempt DESC LIMIT ?`, scope.BranchID, AdmissionClassificationV1, scope.StreamUID, scope.BranchID, scope.ThroughPublicationSequence, scope.BranchID, contextInspectionAttemptLimit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []ContextInspectionAttempt{}
	for rows.Next() {
		var attempt ContextInspectionAttempt
		var finished sql.NullTime
		var eventType, receiptStatus string
		var eventAttempt int64
		var digest []byte
		if err := rows.Scan(&attempt.Attempt, &attempt.ClaimedInputRevision, &attempt.Status, &attempt.TerminalEventID, &finished,
			&attempt.InputEventID, &eventType, &eventAttempt, &receiptStatus, &digest); err != nil {
			return nil, false, err
		}
		attempt.generation = runnerTimingGeneration(stream.OwnerID, stream.UID, attempt.Attempt, digest)
		attempt.ClaimedRevisions = map[int64]bool{attempt.ClaimedInputRevision: true}
		if len(result) == contextInspectionAttemptLimit {
			return result, true, nil
		}
		if attempt.Status != "running" {
			if (!terminalStatus(attempt.Status) && attempt.Status != "reclaimed") || receiptStatus != attempt.Status ||
				eventAttempt != attempt.Attempt || !finished.Valid || (eventType != "runner_finished" && eventType != "runner_reclaimed") {
				return nil, false, ErrEventConflict
			}
			value := finished.Time.UTC()
			attempt.CompletedAt = &value
		} else if finished.Valid || attempt.TerminalEventID != 0 {
			return nil, false, errors.New("invalid running context attempt")
		}
		result = append(result, attempt)
	}
	return result, false, rows.Err()
}
