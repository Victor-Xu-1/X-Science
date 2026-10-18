package transcript

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrMessageIntentConflict    = errors.New("message intent is bound to a different frozen request")
	ErrMessageIntentCancelled   = errors.New("message intent was cancelled")
	ErrMessageIntentUnavailable = errors.New("message intent proof is unavailable")
	ErrFrameMessageStateStale   = errors.New("frame state changed before message admission")
)

// FrameMessageIntent is typed proof on the canonical user event, not a second
// acceptance ledger. The digest covers the complete normalized transport body.
type FrameMessageIntent struct {
	Version            int    `json:"version"`
	OwnerID            string `json:"owner_id"`
	FrameID            string `json:"frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	LoadingID          string `json:"loading_id"`
	InputMessageID     string `json:"input_message_id"`
	TargetBranchID     string `json:"target_branch_id,omitempty"`
	RequestSHA256      string `json:"request_sha256"`
}

// JSONValue keeps fresh queue dedupe and persisted map readback byte-identical.
// Do not change the struct's JSON encoding: existing transcript payloads retain
// their original representation for exact event replay.
func (intent FrameMessageIntent) JSONValue() map[string]any {
	value := map[string]any{
		"version": intent.Version, "owner_id": intent.OwnerID, "frame_id": intent.FrameID,
		"frame_incarnation_id": intent.FrameIncarnationID, "loading_id": intent.LoadingID,
		"input_message_id": intent.InputMessageID, "request_sha256": intent.RequestSHA256,
	}
	if intent.TargetBranchID != "" {
		value["target_branch_id"] = intent.TargetBranchID
	}
	return value
}

type FrameMessageIntentCancellation struct {
	Intent             FrameMessageIntent `json:"intent"`
	Accepted           bool               `json:"accepted"`
	Cancelled          bool               `json:"cancelled"`
	CancellationStatus string             `json:"cancellation_status"`
}

type MessageIntentQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Preserve the established canonical IDs; callers must never derive them from
// turn_id or assume that loading_id itself is the accepted input identity.
func FrameMessageIntentID(frameID, loadingID, targetBranchID string) string {
	if targetBranchID != "" {
		return "branch-continue:" + loadingID
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("x-science:frame-message:"+frameID+"\x00"+loadingID)).String()
}

func (intent FrameMessageIntent) Valid() bool {
	digest, err := hex.DecodeString(intent.RequestSHA256)
	return intent.Version == 1 && intent.OwnerID != "" && intent.FrameID != "" && intent.FrameIncarnationID != "" &&
		strings.TrimSpace(intent.LoadingID) == intent.LoadingID && intent.LoadingID != "" && len(intent.LoadingID) <= 512 &&
		(intent.TargetBranchID == "" || IsValidBranchID(intent.TargetBranchID)) && err == nil && len(digest) == 32 &&
		intent.InputMessageID == FrameMessageIntentID(intent.FrameID, intent.LoadingID, intent.TargetBranchID)
}

func DecodeFrameMessageIntent(value any) (*FrameMessageIntent, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	var intent FrameMessageIntent
	if err != nil || json.Unmarshal(raw, &intent) != nil || !intent.Valid() {
		return nil, ErrMessageIntentUnavailable
	}
	return &intent, nil
}

func ValidateFrameMessageIntentScope(ctx context.Context, db MessageIntentQuery, intent FrameMessageIntent) error {
	if !intent.Valid() {
		return ErrMessageIntentUnavailable
	}
	var owner, incarnation string
	err := db.QueryRowContext(ctx, `SELECT project.user_id,frame.incarnation_id FROM frames frame
		JOIN projects project ON project.id=frame.project_id WHERE frame.id=?`, intent.FrameID).Scan(&owner, &incarnation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOwnerMismatch
	}
	if err != nil {
		return err
	}
	if owner != intent.OwnerID {
		return ErrOwnerMismatch
	}
	if incarnation != intent.FrameIncarnationID {
		return ErrMessageIntentConflict
	}
	return nil
}

func messageIntentCandidateIDs(intent FrameMessageIntent) []string {
	return []string{FrameMessageIntentID(intent.FrameID, intent.LoadingID, ""), FrameMessageIntentID(intent.FrameID, intent.LoadingID, "branch")}
}

// Uses the canonical client-message index, bounded to the two historical ID
// forms. A changed branch mode cannot reuse a frozen mutation as another task.
func ReadAcceptedFrameMessageIntent(ctx context.Context, db MessageIntentQuery, intent FrameMessageIntent) (bool, error) {
	var streamUID string
	err := db.QueryRowContext(ctx, `SELECT active_stream_uid FROM transcript_frame_authority
		WHERE owner_id=? AND session_id=? AND read_authority='transcript_payload_v1' AND write_authority='transcript_payload_v1'`, intent.OwnerID, intent.FrameID).Scan(&streamUID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrMessageIntentUnavailable
	}
	if err != nil {
		return false, err
	}
	for _, id := range messageIntentCandidateIDs(intent) {
		var raw, source, kind string
		err := db.QueryRowContext(ctx, `SELECT COALESCE(payload_json,''),source,event_type FROM transcript_events WHERE stream_uid=? AND client_message_id=?`, streamUID, id).Scan(&raw, &source, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		var payload struct {
			Intent *FrameMessageIntent `json:"messageIntent"`
		}
		if source != string(EventSourcePayload) || (kind != "user_message" && kind != "user_input_response") || json.Unmarshal([]byte(raw), &payload) != nil || payload.Intent == nil || !payload.Intent.Valid() {
			return false, ErrMessageIntentUnavailable
		}
		if *payload.Intent != intent || id != intent.InputMessageID {
			return false, ErrMessageIntentConflict
		}
		return true, nil
	}
	return false, nil
}

func (receipt FrameMessageIntentCancellation) Valid() bool {
	if !receipt.Intent.Valid() {
		return false
	}
	switch receipt.CancellationStatus {
	case "cancelled_before_acceptance":
		return !receipt.Accepted && receipt.Cancelled
	case "cancelled":
		return receipt.Accepted && receipt.Cancelled
	case "already_terminal":
		return receipt.Accepted && !receipt.Cancelled
	default:
		return false
	}
}
