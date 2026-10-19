package sessionagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

type Binding struct {
	Version         int       `json:"version"`
	OwnerID         string    `json:"owner_id"`
	Scope           Scope     `json:"scope"`
	Branch          Branch    `json:"branch"`
	Selection       Selection `json:"selection"`
	ClientMessageID string    `json:"client_message_id"`
	StreamUID       string    `json:"stream_uid"`
	StreamEpoch     int64     `json:"stream_epoch"`
	Attempt         int64     `json:"attempt"`
}

func ResolveScope(ctx context.Context, tx Transaction, owner, frame string) (Scope, error) {
	var result Scope
	var actualOwner string
	err := tx.QueryRowContext(ctx, `SELECT p.user_id,f.id,f.incarnation_id,r.id,r.incarnation_id
		FROM frames f JOIN projects p ON p.id=f.project_id JOIN frames r ON r.id=f.root_frame_id AND r.project_id=f.project_id
		WHERE f.id=?`, frame).Scan(&actualOwner, &result.FrameID, &result.FrameIncarnationID, &result.RootFrameID, &result.RootIncarnationID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && actualOwner != owner {
		return Scope{}, ErrTargetUnavailable
	}
	if err != nil {
		return Scope{}, fmt.Errorf("%w: scope: %w", ErrUnavailable, err)
	}
	if !ValidScope(result) {
		return Scope{}, ErrScopeStale
	}
	return result, nil
}

func ActiveBranch(ctx context.Context, tx Transaction, owner, frame string) (Branch, error) {
	var result Branch
	err := tx.QueryRowContext(ctx, `SELECT b.active_branch_id,b.generation FROM transcript_frame_authority a
		JOIN transcript_streams s ON s.stream_uid=a.active_stream_uid AND s.epoch=a.active_epoch AND s.owner_id=a.owner_id
		JOIN transcript_branch_state b ON b.stream_uid=s.stream_uid
		WHERE a.owner_id=? AND a.session_id=? AND s.frame_id=?`, owner, frame, frame).Scan(&result.ID, &result.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("%w: active branch: %w", ErrUnavailable, err)
	}
	return result, nil
}

func Established(ctx context.Context, tx Transaction, frame string) (bool, error) {
	var established bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM frame_events WHERE frame_id=? AND event_type IN (?,?,?,?,?,?))`, frame, HeadEventType, ReceiptEventType, SnapshotEventType, InputEventType, ClaimEventType, AppliedEventType).Scan(&established)
	return established, err
}

// A pre-stream Guid selection may bind the first prospective canonical input,
// never a reset/recreated epoch or an old branch. The original command stays
// immutable; only the current head acquires this newly materialized branch.
func ReferenceMatches(ctx context.Context, tx Transaction, owner string, head Head, reference InputReference, branch Branch) (bool, error) {
	if reference.Scope != head.Scope || reference.Revision != head.Revision || reference.SnapshotID != head.Desired.SnapshotID {
		return false, nil
	}
	if reference.Branch == branch && head.Branch == branch {
		return true, nil
	}
	if reference.Branch != (Branch{}) || head.Branch != (Branch{}) || branch.Generation != 1 {
		return false, nil
	}
	var first bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_frame_authority a JOIN transcript_streams s
		ON s.stream_uid=a.active_stream_uid AND s.epoch=a.active_epoch AND s.owner_id=a.owner_id
		JOIN transcript_branch_state b ON b.stream_uid=s.stream_uid
		WHERE a.owner_id=? AND a.session_id=? AND a.active_epoch=1 AND s.input_revision=0
		AND b.active_branch_id=? AND b.generation=1 AND NOT EXISTS(SELECT 1 FROM transcript_runner_attempts r WHERE r.stream_uid=s.stream_uid))`, owner, head.Scope.FrameID, branch.ID).Scan(&first)
	return first, err
}

// Admit binds a new ordinary input only. Caller has already handled accepted
// replay and classification; hidden/control/approval inputs may not select.
func Admit(ctx context.Context, tx Transaction, owner, frame, clientID string, branch Branch, reference *InputReference, now time.Time) error {
	established, err := Established(ctx, tx, frame)
	if err != nil {
		return err
	}
	if !established && reference == nil {
		return nil
	}
	scope, err := ResolveScope(ctx, tx, owner, frame)
	if err != nil {
		return err
	}
	head, previous, _, err := ReadHead(ctx, tx, owner, scope)
	if err != nil {
		return err
	}
	selection := head.Applied
	if selection.SnapshotID == "" {
		selection = head.Fallback
	}
	if reference != nil {
		matches, err := ReferenceMatches(ctx, tx, owner, head, *reference, branch)
		if err != nil {
			return err
		}
		if !matches {
			return ErrRevisionConflict
		}
		if head.Branch != branch {
			head.Branch = branch
			if err := writeRecord(ctx, tx, scope, headID(owner, scope), HeadEventType, previous, head, now); err != nil {
				return err
			}
		}
		selection = head.Desired
	}
	if selection.SnapshotID == "" {
		return ErrTargetUnavailable
	}
	definition, err := ReadSnapshot(ctx, tx, owner, scope, selection.SnapshotID)
	if err != nil {
		return err
	}
	if err := DefinitionAvailable(ctx, tx, owner, definition); err != nil {
		return err
	}
	record := Binding{Version: 1, OwnerID: owner, Scope: scope, Branch: branch, Selection: selection, ClientMessageID: clientID}
	return writeRecord(ctx, tx, scope, RecordID("input", owner, scope.FrameIncarnationID, clientID), InputEventType, "", record, now)
}

// New inputs consult only tx-aware availability, not today's mutable prompt or
// settings. Accepted replay never reaches this guard. Actual execution retains
// its accepted snapshot even if a profile is later disabled or removed.
func DefinitionAvailable(ctx context.Context, tx Transaction, owner string, definition Snapshot) error {
	for _, entry := range []struct{ id, name string }{{definition.SelectionProfileID, definition.SelectionName}, {func() string {
		if definition.Profile != nil {
			return definition.DefinitionID
		}
		return ""
	}(), definition.AgentName}} {
		if entry.id == "" {
			continue
		}
		var enabled bool
		err := tx.QueryRowContext(ctx, `SELECT enabled FROM user_agents WHERE id=? AND user_id=? AND name=?`, entry.id, owner, entry.name).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
			return ErrTargetUnavailable
		}
		if err != nil {
			return fmt.Errorf("%w: definition availability: %w", ErrUnavailable, err)
		}
	}
	return nil
}

func readBinding(ctx context.Context, tx Transaction, owner string, scope Scope, id, kind string) (*Binding, error) {
	var record Binding
	_, found, err := readRecord(ctx, tx, scope, id, kind, &record)
	if err != nil || !found {
		return nil, err
	}
	if record.Version != 1 || record.OwnerID != owner || record.Scope != scope || record.ClientMessageID == "" || record.Selection.Revision < 0 || record.Selection.SnapshotID == "" || record.Selection.AgentName == "" {
		return nil, ErrUnavailable
	}
	return &record, nil
}

func ReadInput(ctx context.Context, tx Transaction, owner string, scope Scope, clientID string) (*Binding, error) {
	return readBinding(ctx, tx, owner, scope, RecordID("input", owner, scope.FrameIncarnationID, clientID), InputEventType)
}

func ReadClaim(ctx context.Context, tx Transaction, owner string, scope Scope, uid string, epoch, attempt int64) (*Binding, error) {
	binding, err := readBinding(ctx, tx, owner, scope, RecordID("claim", owner, scope.FrameIncarnationID, uid, strconv.FormatInt(epoch, 10), strconv.FormatInt(attempt, 10)), ClaimEventType)
	if err != nil || binding == nil {
		return binding, err
	}
	if binding.StreamUID != uid || binding.StreamEpoch != epoch || binding.Attempt != attempt {
		return nil, ErrUnavailable
	}
	return binding, nil
}

// BindClaim is called by the existing common claim transaction after all live,
// resume and Fresh-admission fences. It cannot reconfigure a running attempt.
func BindClaim(ctx context.Context, tx Transaction, owner string, scope Scope, uid string, epoch, attempt int64, binding *Binding, now time.Time) error {
	if binding == nil {
		return nil
	}
	if binding.OwnerID != owner || binding.Scope != scope {
		return ErrScopeStale
	}
	definition, err := ReadSnapshot(ctx, tx, owner, scope, binding.Selection.SnapshotID)
	if err != nil {
		return err
	}
	if definition.AgentName != binding.Selection.AgentName {
		return ErrUnavailable
	}
	record := *binding
	record.StreamUID, record.StreamEpoch, record.Attempt = uid, epoch, attempt
	if err := writeRecord(ctx, tx, scope, RecordID("claim", owner, scope.FrameIncarnationID, uid, strconv.FormatInt(epoch, 10), strconv.FormatInt(attempt, 10)), ClaimEventType, "", record, now); err != nil {
		return err
	}
	return nil
}

// Applied is separate from claim binding: only the existing profile resolver's
// successful readback may report application. A desired receipt is not enough.
func Applied(ctx context.Context, tx Transaction, owner string, binding Binding, now time.Time) error {
	head, previous, _, err := ReadHead(ctx, tx, owner, binding.Scope)
	if err != nil {
		return err
	}
	if binding.OwnerID != owner || binding.Selection.Revision > head.Revision {
		return ErrScopeStale
	}
	if binding.Selection == head.Applied {
		return nil
	}
	// The compatibility identity moves only after the existing resolver prepared
	// this exact claim. A failed preparation leaves the previous identity intact.
	result, err := tx.ExecContext(ctx, `UPDATE frames SET agent_name=? WHERE id=? AND incarnation_id=?`, binding.Selection.AgentName, binding.Scope.FrameID, binding.Scope.FrameIncarnationID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrScopeStale
	}
	head.Applied = binding.Selection
	if head.Applied == head.Desired {
		head.PreparationError = ""
	}
	return writeRecord(ctx, tx, binding.Scope, headID(owner, binding.Scope), HeadEventType, previous, head, now)
}

func PreparationFailed(ctx context.Context, tx Transaction, owner string, binding Binding, now time.Time) error {
	head, previous, _, err := ReadHead(ctx, tx, owner, binding.Scope)
	if err != nil {
		return err
	}
	if binding.OwnerID != owner || binding.Selection != head.Desired {
		return nil
	}
	head.PreparationError = "SESSION_SPECIALIST_PREPARATION_FAILED"
	return writeRecord(ctx, tx, binding.Scope, headID(owner, binding.Scope), HeadEventType, previous, head, now)
}
