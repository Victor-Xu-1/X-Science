package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Admission and retention use the same typed-envelope validity rule. A bare
// marker does not make an ordinary transport receipt permanent.
func isDurableAsideLifecycleRecord(record CompatibilityMessageIntent) bool {
	var request struct {
		Version        int     `json:"_aside_contract"`
		OwnerID        string  `json:"owner_id"`
		ParentID       string  `json:"parent_frame_id"`
		Request        string  `json:"request"`
		Model          *string `json:"model"`
		AsSession      bool    `json:"as_session"`
		SourceBranchID string  `json:"source_branch_id"`
		Generation     int64   `json:"expected_generation"`
	}
	raw, err := json.Marshal(record.DedupePayload)
	if err != nil || json.Unmarshal(raw, &request) != nil || request.Version != 1 || request.OwnerID == "" || request.ParentID == "" || request.Request == "" || record.IntentID == "" ||
		(request.SourceBranchID == "") != (request.Generation == 0) || request.Generation < 0 || request.SourceBranchID != "" && !transcriptstore.IsValidBranchID(request.SourceBranchID) {
		return false
	}
	canonical, _ := json.Marshal(compatibilityAsideIntentPayload(CompatibilityAsideInput{ParentRootFrameID: request.ParentID, Request: request.Request, Model: request.Model, AsSession: request.AsSession, SourceBranchID: request.SourceBranchID, ExpectedGeneration: request.Generation}, request.OwnerID))
	if !bytes.Equal(raw, canonical) {
		return false
	}
	if record.State == "drained" {
		var accepted CompatibilityAsideAcceptance
		raw, err = json.Marshal(record.DeliveryPayload["aside_acceptance"])
		return err == nil && json.Unmarshal(raw, &accepted) == nil && accepted.OwnerID == request.OwnerID && accepted.ParentRootFrameID == request.ParentID && accepted.ParentIncarnationID != "" &&
			accepted.IntentID == record.IntentID && accepted.FrameID == record.FrameID && accepted.FrameID != "" && accepted.FrameID != accepted.ParentRootFrameID && accepted.InputMessageID != "" &&
			transcriptstore.IsValidBranchID(accepted.SourceBranchID) && accepted.SourceGeneration > 0 && accepted.ThroughPublication >= 0 && accepted.PrefixLen >= 0 && accepted.AsSession == request.AsSession && record.DeliveryPayload["aside_cancellation"] == nil &&
			(request.SourceBranchID == "" || accepted.SourceBranchID == request.SourceBranchID && accepted.SourceGeneration == request.Generation)
	}
	if record.State == "retracted" {
		var cancelled CompatibilityAsideCancellation
		raw, err = json.Marshal(record.DeliveryPayload["aside_cancellation"])
		return err == nil && json.Unmarshal(raw, &cancelled) == nil && cancelled.BeforeAcceptance && cancelled.OwnerID == request.OwnerID && cancelled.ParentRootFrameID == request.ParentID &&
			cancelled.ParentIncarnationID != "" && cancelled.ParentRootFrameID == record.FrameID && cancelled.IntentID == record.IntentID && record.DeliveryPayload["aside_acceptance"] == nil
	}
	return false
}

type resolvedQueuedIntentCandidate struct {
	Sequence int64
	IntentID string
	FrameID  string
	Payload  string
	State    string
}

// Classify outside the sweep's write transaction. Permanent lifecycle receipts
// may grow with history, but scanning/decoding them must never hold its write lock.
func resolvedQueuedIntentCandidates(ctx context.Context, db *sql.DB, cutoff time.Time) ([]resolvedQueuedIntentCandidate, error) {
	var candidates []resolvedQueuedIntentCandidate
	var after int64
	for {
		rows, err := db.QueryContext(ctx, `SELECT sequence,intent_id,frame_id,payload,state FROM queued_user_messages
			WHERE state IN ('drained','retracted') AND resolved_at IS NOT NULL AND resolved_at<? AND sequence>?
			ORDER BY sequence LIMIT 256`, cutoff, after)
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next() {
			var record CompatibilityMessageIntent
			var candidate resolvedQueuedIntentCandidate
			if err := rows.Scan(&candidate.Sequence, &candidate.IntentID, &candidate.FrameID, &candidate.Payload, &candidate.State); err != nil {
				rows.Close()
				return nil, err
			}
			after = candidate.Sequence
			count++
			var envelope compatibilityQueueEnvelope
			valid := json.Unmarshal([]byte(candidate.Payload), &envelope) == nil && envelope.Version == 1
			record.Sequence, record.IntentID, record.FrameID, record.State = candidate.Sequence, candidate.IntentID, candidate.FrameID, candidate.State
			record.DedupePayload, record.DeliveryPayload = envelope.DedupePayload, envelope.DeliveryPayload
			if !valid || !(isDurableAsideLifecycleRecord(record) || isDurableMessageIntentCancellation(record)) {
				candidates = append(candidates, candidate)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if count < 256 {
			return candidates, nil
		}
	}
}

func sweepResolvedQueuedIntents(ctx context.Context, tx *sql.Tx, cutoff time.Time, candidates []resolvedQueuedIntentCandidate) (int64, error) {
	var deleted int64
	for _, candidate := range candidates {
		// Recheck the exact classified payload and identity after acquiring the
		// write lock. Requeued, refreshed or replaced rows remain untouched.
		n, err := lifecycleDelete(ctx, tx, `DELETE FROM queued_user_messages
			WHERE sequence=? AND intent_id=? AND frame_id=? AND payload=? AND state=?
				AND state IN ('drained','retracted') AND resolved_at IS NOT NULL AND resolved_at<?`,
			candidate.Sequence, candidate.IntentID, candidate.FrameID, candidate.Payload, candidate.State, cutoff)
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	return deleted, nil
}
