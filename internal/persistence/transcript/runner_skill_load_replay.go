package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var ErrSkillLoadReplayBudget = errors.New("transcript Skill load replay exceeds its scan budget")

// ListRunnerSkillLoadCheckpointsInput pins a read to the current runner's exact
// input and projection. It grants no execution or historical-branch authority.
type ListRunnerSkillLoadCheckpointsInput struct {
	StreamUID, OwnerID, BranchID string
	BranchGeneration             int64
	ThroughPublicationSequence   int64
	InputRevision                int64
	ThroughAttempt               int64
	SkillName, InvocationKey     string
	FullResultOnly               bool
	Limit                        int
}

// ListRunnerSkillLoadCheckpoints reads original terminal checkpoints, newest
// publication first, independently of compaction and continuity summaries.
// Completed here describes the checkpoint phase, not the tool result outcome:
// callers must classify each original result before trusting a load receipt.
func (r *Repository) ListRunnerSkillLoadCheckpoints(
	ctx context.Context, input ListRunnerSkillLoadCheckpointsInput,
) ([]RunnerReplayEvent, error) {
	root := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(input.SkillName), "/"))
	if root == "" || input.InputRevision <= 0 || input.ThroughAttempt <= 0 || input.Limit < 1 || input.Limit > 32 {
		return nil, ErrEventConflict
	}
	result := []RunnerReplayEvent{}
	err := r.readFrozenSkillProjection(ctx, ListProjectedEventsInput{
		StreamUID: input.StreamUID, OwnerID: input.OwnerID, BranchID: input.BranchID,
		BranchGeneration: input.BranchGeneration, ThroughPublicationSequence: input.ThroughPublicationSequence,
	}, input.InputRevision, input.ThroughAttempt, func(tx *sql.Tx, kind StreamKind, frameID string, snapshot ProjectionSnapshot) error {
		rows, err := tx.QueryContext(ctx, `WITH candidates AS (
			SELECT event.stream_uid,event.event_id,event.publication_seq,event.client_message_id,event.event_type,event.source,
				event.runner_attempt,event.payload_json,event.frame_event_id,event.created_at,
				frame.frame_id AS canonical_frame_id,frame.event_type AS canonical_type,frame.payload AS canonical_payload,
				CASE WHEN event.source='frame_ref' THEN frame.payload ELSE event.payload_json END AS body
			FROM transcript_branch_events membership
			JOIN transcript_events event ON event.stream_uid=membership.stream_uid AND event.event_id=membership.event_id
			JOIN transcript_runner_attempts attempt ON attempt.stream_uid=event.stream_uid AND attempt.attempt=event.runner_attempt
			LEFT JOIN frame_events frame ON event.source='frame_ref' AND frame.id=event.frame_event_id
			WHERE membership.stream_uid=? AND membership.branch_id=? AND event.publication_seq<=?
				AND event.event_type='runner_checkpoint' AND attempt.claimed_input_revision=? AND attempt.attempt<=?
		)
		SELECT stream_uid,event_id,publication_seq,client_message_id,event_type,source,
			runner_attempt,payload_json,frame_event_id,created_at,canonical_frame_id,canonical_type,canonical_payload
		FROM candidates
		WHERE (source='frame_ref' AND (canonical_frame_id IS NULL OR canonical_frame_id<>? OR
			canonical_type IS NULL OR canonical_type<>event_type OR canonical_payload IS NULL)) OR (
			json_extract(body,'$.status')='completed' AND json_extract(body,'$.toolPhase')='completed'
			AND lower(trim(json_extract(body,'$.toolName'),?))='skill'
			AND (?='' OR (json_type(body,'$.skillLoadReceipt.invocationKey')='text'
				AND json_extract(body,'$.skillLoadReceipt.invocationKey')=?))
			AND (?=0 OR json_type(body,'$.toolResult')='text' OR json_type(body,'$.toolResult.prompt')='text' OR
				(json_type(body,'$.toolResult.artifact_id')='text' AND json_type(body,'$.toolResult.version_id')='text'))
		)
		ORDER BY publication_seq DESC LIMIT ?`, snapshot.StreamUID, snapshot.BranchID, snapshot.ThroughPublicationSequence,
			input.InputRevision, input.ThroughAttempt, frameID,
			"\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000",
			input.InvocationKey, input.InvocationKey, input.FullResultOnly, DefaultRunnerReplayExpandedEvents+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var scanned int
		var scannedBytes int64
		for rows.Next() {
			event, payload, err := scanProjectedEvent(rows, kind, frameID)
			if err != nil {
				return err
			}
			scanned++
			scannedBytes += int64(len(payload))
			if scanned > DefaultRunnerReplayExpandedEvents || scannedBytes > DefaultRunnerReplayExpandedBytes {
				return ErrSkillLoadReplayBudget
			}
			var message map[string]json.RawMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				return ErrEventConflict
			}
			var toolName string
			if err := json.Unmarshal(message["toolName"], &toolName); err != nil {
				return ErrEventConflict
			}
			if name := strings.TrimSpace(toolName); name != "skill" && name != "Skill" {
				continue
			}
			originalInput, executedPresent := message["executedToolInput"]
			if !executedPresent {
				originalInput = message["toolInput"]
			}
			var executed map[string]json.RawMessage
			var skillName string
			// A present but malformed execution input must not fall back to the
			// model request. Unknown roots remain barriers for every requested root.
			if json.Unmarshal(originalInput, &executed) == nil {
				_ = json.Unmarshal(executed["skill"], &skillName)
			}
			candidateRoot := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(skillName), "/"))
			if candidateRoot != "" && candidateRoot != root {
				continue
			}
			result = append(result, RunnerReplayEvent{Event: event, ResolvedPayloadJSON: payload})
			if len(result) == input.Limit {
				break
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		eventIDs := make([]int64, 0, len(result))
		for _, event := range result {
			eventIDs = append(eventIDs, event.Event.EventID)
		}
		outcomes, err := projectedToolOutcomesTx(ctx, tx, ListProjectedEventsInput{
			StreamUID: snapshot.StreamUID, OwnerID: input.OwnerID, BranchID: snapshot.BranchID,
			BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
		}, snapshot, eventIDs)
		if err != nil {
			return err
		}
		for index := range result {
			result[index].ToolOutcome = outcomes[result[index].Event.EventID]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetProjectedEventByID resolves one exact event inside the caller's frozen
// active projection. Event IDs never substitute for publication coordinates.
func (r *Repository) GetProjectedEventByID(
	ctx context.Context, input ListProjectedEventsInput, eventID int64,
) (ProjectedEvent, bool, error) {
	if eventID <= 0 {
		return ProjectedEvent{}, false, ErrEventConflict
	}
	var result ProjectedEvent
	var found bool
	err := r.readFrozenSkillProjection(ctx, input, 0, 0, func(tx *sql.Tx, kind StreamKind, frameID string, snapshot ProjectionSnapshot) error {
		event, payload, err := scanProjectedEvent(tx.QueryRowContext(ctx, `
			SELECT event.stream_uid,event.event_id,event.publication_seq,event.client_message_id,event.event_type,event.source,
				event.runner_attempt,event.payload_json,event.frame_event_id,event.created_at,frame.frame_id,frame.event_type,frame.payload
			FROM transcript_branch_events membership
			JOIN transcript_events event ON event.stream_uid=membership.stream_uid AND event.event_id=membership.event_id
			LEFT JOIN frame_events frame ON event.source='frame_ref' AND frame.id=event.frame_event_id
			WHERE membership.stream_uid=? AND membership.branch_id=? AND event.event_id=?
				AND event.publication_seq>? AND event.publication_seq<=?`, snapshot.StreamUID, snapshot.BranchID,
			eventID, input.AfterPublicationSequence, snapshot.ThroughPublicationSequence), kind, frameID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = ProjectedEvent{Event: event, ResolvedPayloadJSON: payload, ArtifactReferences: []ArtifactReference{}}
		if input.IncludeToolOutcomes {
			outcomes, err := projectedToolOutcomesTx(ctx, tx, input, snapshot, []int64{eventID})
			if err != nil {
				return err
			}
			result.ToolOutcome = outcomes[eventID]
		}
		found = true
		return nil
	})
	if err != nil {
		return ProjectedEvent{}, false, err
	}
	return result, found, nil
}

// Both readers validate ownership and the current active branch in the very
// transaction that decodes the source. A caller-supplied older cutoff remains
// fixed; a newer branch, generation, attempt, or input is never adopted here.
func (r *Repository) readFrozenSkillProjection(
	ctx context.Context, input ListProjectedEventsInput, inputRevision, throughAttempt int64,
	read func(*sql.Tx, StreamKind, string, ProjectionSnapshot) error,
) error {
	db := r.readDatabase()
	if db == nil {
		return ErrSchemaUnavailable
	}
	input.StreamUID, input.OwnerID, input.BranchID = strings.TrimSpace(input.StreamUID), strings.TrimSpace(input.OwnerID), strings.TrimSpace(input.BranchID)
	if input.StreamUID == "" || input.OwnerID == "" || !validTranscriptBranchID(input.BranchID) ||
		input.BranchGeneration <= 0 || input.ThroughPublicationSequence <= 0 || input.AfterPublicationSequence < 0 ||
		input.AfterPublicationSequence > input.ThroughPublicationSequence || (inputRevision == 0) != (throughAttempt == 0) ||
		inputRevision < 0 || throughAttempt < 0 {
		return ErrEventConflict
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	kind, frameID, snapshot, err := projectionSnapshotTx(ctx, tx, input.StreamUID, input.OwnerID)
	if err != nil {
		return schemaError(err)
	}
	if snapshot.BranchID != input.BranchID || snapshot.BranchGeneration != input.BranchGeneration ||
		input.ThroughPublicationSequence > snapshot.ThroughPublicationSequence {
		return ErrBranchStateStale
	}
	if throughAttempt > 0 {
		var currentAttempt, currentRevision int64
		err := tx.QueryRowContext(ctx, `SELECT attempt,claimed_input_revision FROM transcript_runner_attempts
			WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, snapshot.StreamUID).Scan(&currentAttempt, &currentRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrClaimStale
		}
		if err != nil {
			return schemaError(err)
		}
		if currentAttempt != throughAttempt || currentRevision != inputRevision {
			return ErrClaimStale
		}
	}
	snapshot.ThroughPublicationSequence = input.ThroughPublicationSequence
	if err := read(tx, StreamKind(kind), frameID, snapshot); err != nil {
		if errors.Is(err, ErrSkillLoadReplayBudget) {
			return err
		}
		return schemaError(err)
	}
	return schemaError(tx.Commit())
}
