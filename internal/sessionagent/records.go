package sessionagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Transaction is implemented by the existing Transcript immediate transaction
// and SQL read snapshot. This package owns no connection, scheduler or schema.
type Transaction interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Head struct {
	Version             int       `json:"version"`
	OwnerID             string    `json:"owner_id"`
	Scope               Scope     `json:"scope"`
	Branch              Branch    `json:"branch"`
	Revision            int64     `json:"revision"`
	Desired             Selection `json:"desired"`
	Applied             Selection `json:"applied"`
	Fallback            Selection `json:"fallback"`
	FallbackUnavailable bool      `json:"fallback_unavailable,omitempty"`
	PreparationError    string    `json:"preparation_error,omitempty"`
}

type snapshotRecord struct {
	Version  int      `json:"version"`
	OwnerID  string   `json:"owner_id"`
	Scope    Scope    `json:"scope"`
	Snapshot Snapshot `json:"snapshot"`
}

type commandRecord struct {
	Version int     `json:"version"`
	OwnerID string  `json:"owner_id"`
	Receipt Receipt `json:"receipt"`
}

func headID(owner string, scope Scope) string {
	return RecordID("head", owner, scope.FrameID, scope.FrameIncarnationID)
}

func readRecord(ctx context.Context, tx Transaction, scope Scope, id, kind string, out any) (string, bool, error) {
	var frame, actualKind, raw string
	err := tx.QueryRowContext(ctx, `SELECT frame_id,event_type,payload FROM frame_events WHERE id=?`, id).Scan(&frame, &actualKind, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: read record: %w", ErrUnavailable, err)
	}
	if frame != scope.FrameID || actualKind != kind || DecodeRecord([]byte(raw), out) != nil {
		return "", false, ErrUnavailable
	}
	return raw, true, nil
}

func writeRecord(ctx context.Context, tx Transaction, scope Scope, id, kind, previous string, record any, now time.Time) error {
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > 3<<20 {
		return ErrUnavailable
	}
	if previous != "" {
		result, err := tx.ExecContext(ctx, `UPDATE frame_events SET payload=? WHERE id=? AND frame_id=? AND event_type=? AND payload=?`, string(raw), id, scope.FrameID, kind, previous)
		if err != nil {
			return fmt.Errorf("%w: update record: %w", ErrUnavailable, err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return ErrRevisionConflict
		}
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at)
		VALUES(?,?,(SELECT COALESCE(MAX(sequence),0)+1 FROM frame_events WHERE frame_id=?),?,?,?)`, id, scope.FrameID, scope.FrameID, kind, string(raw), now)
	if err != nil {
		return fmt.Errorf("%w: insert record: %w", ErrUnavailable, err)
	}
	return nil
}

func ReadHead(ctx context.Context, tx Transaction, owner string, scope Scope) (Head, string, bool, error) {
	var head Head
	raw, found, err := readRecord(ctx, tx, scope, headID(owner, scope), HeadEventType, &head)
	if err != nil {
		return head, "", false, err
	}
	if !found {
		var established bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM frame_events WHERE frame_id=? AND event_type IN (?,?,?,?,?,?))`, scope.FrameID,
			HeadEventType, ReceiptEventType, SnapshotEventType, InputEventType, ClaimEventType, AppliedEventType).Scan(&established)
		if err != nil {
			return head, "", false, fmt.Errorf("%w: inspect records: %w", ErrUnavailable, err)
		}
		if established {
			return head, "", false, ErrUnavailable
		}
		return Head{Version: 1, OwnerID: owner, Scope: scope}, "", false, nil
	}
	if head.Version != 1 || head.OwnerID != owner || head.Scope != scope || head.Revision < 1 || head.Revision > MaxRevision ||
		head.Desired.Revision != head.Revision || head.Desired.SnapshotID == "" || head.Desired.AgentName == "" ||
		head.Applied.Revision < 0 || head.Applied.Revision > head.Revision {
		return head, "", false, ErrUnavailable
	}
	return head, raw, true, nil
}

func ReadSnapshot(ctx context.Context, tx Transaction, owner string, scope Scope, id string) (Snapshot, error) {
	var record snapshotRecord
	_, found, err := readRecord(ctx, tx, scope, RecordID("definition", owner, scope.FrameIncarnationID, id), SnapshotEventType, &record)
	if err != nil {
		return Snapshot{}, err
	}
	if !found || record.Version != 1 || record.OwnerID != owner || record.Scope != scope || record.Snapshot.ID != id || ValidateSnapshot(record.Snapshot) != nil {
		return Snapshot{}, ErrUnavailable
	}
	return record.Snapshot, nil
}

// LookupReceipt is read-only and is used only by the typed host adapter to
// reconstruct its original internally generated revision after a lost ACK.
func LookupReceipt(ctx context.Context, tx Transaction, owner string, scope Scope, commandID string) (Receipt, bool, error) {
	var record commandRecord
	_, found, err := readRecord(ctx, tx, scope, RecordID("command", owner, scope.FrameIncarnationID, commandID), ReceiptEventType, &record)
	if err != nil || !found {
		return Receipt{}, found, err
	}
	r := record.Receipt
	if record.Version != 1 || record.OwnerID != owner || ValidateCommand(r.Command) != nil || r.Command.Scope != scope || r.Command.CommandID != commandID || r.AcceptedRevision != r.Command.ExpectedRevision+1 || r.SnapshotID == "" || r.AcceptedAt.IsZero() {
		return Receipt{}, false, ErrUnavailable
	}
	return r, true, nil
}

// Prepare is called only inside the canonical immediate transaction after
// ownership/lifetime and catalog availability checks by the server/store seam.
// Replays precede revision/catalog checks and never rewind current readback.
func Prepare(ctx context.Context, tx Transaction, owner string, command Command, capture func(bool) (Preparation, error), now time.Time) (Mutation, error) {
	result := Mutation{Schema: MutationSchema}
	if ValidateCommand(command) != nil {
		return result, ErrInvalid
	}
	head, previous, _, err := ReadHead(ctx, tx, owner, command.Scope)
	if err != nil {
		return result, err
	}
	commandID := RecordID("command", owner, command.Scope.FrameIncarnationID, command.CommandID)
	var record commandRecord
	_, replay, err := readRecord(ctx, tx, command.Scope, commandID, ReceiptEventType, &record)
	if err != nil {
		return result, err
	}
	if replay {
		receipt := record.Receipt
		if record.Version != 1 || record.OwnerID != owner || ValidateCommand(receipt.Command) != nil || receipt.Command.Scope != command.Scope || receipt.Command.CommandID != command.CommandID || receipt.AcceptedRevision != receipt.Command.ExpectedRevision+1 || receipt.SnapshotID == "" || receipt.AcceptedAt.IsZero() {
			return result, ErrUnavailable
		}
		if receipt.Command != command {
			return result, ErrCommandConflict
		}
		result.Receipt, result.Replayed = receipt, true
		result.Snapshot = readback(head)
		return result, nil
	}
	if command.ExpectedRevision != head.Revision {
		return result, ErrRevisionConflict
	}
	if capture == nil {
		return result, ErrInvalid
	}
	prepared, err := capture(head.Revision == 0)
	if err != nil {
		return result, err
	}
	definition := prepared.Desired
	if ValidateSnapshot(definition) != nil || !command.UseDefault && definition.SelectionName != command.DesiredAgent {
		return result, ErrInvalid
	}
	if head.Revision == 0 {
		fallback := prepared.Fallback
		if fallback == nil {
			if !prepared.FallbackUnavailable {
				return result, ErrUnavailable
			}
			head.FallbackUnavailable = true
		} else {
			if ValidateSnapshot(*fallback) != nil {
				return result, ErrUnavailable
			}
			if err := writeRecord(ctx, tx, command.Scope, RecordID("definition", owner, command.Scope.FrameIncarnationID, fallback.ID), SnapshotEventType, "", snapshotRecord{1, owner, command.Scope, *fallback}, now); err != nil {
				return result, err
			}
			head.Fallback = selectionFrom(*fallback, 0)
		}
	}
	snapshotID := RecordID("definition", owner, command.Scope.FrameIncarnationID, definition.ID)
	var old snapshotRecord
	_, exists, err := readRecord(ctx, tx, command.Scope, snapshotID, SnapshotEventType, &old)
	if err != nil {
		return result, err
	}
	if exists {
		if old.Version != 1 || old.OwnerID != owner || old.Scope != command.Scope || ValidateSnapshot(old.Snapshot) != nil || old.Snapshot.ID != definition.ID {
			return result, ErrUnavailable
		}
	} else if err := writeRecord(ctx, tx, command.Scope, snapshotID, SnapshotEventType, "", snapshotRecord{1, owner, command.Scope, definition}, now); err != nil {
		return result, err
	}
	head.Revision++
	head.PreparationError = ""
	head.Branch = command.Branch
	head.Desired = selectionFrom(definition, head.Revision)
	if err := writeRecord(ctx, tx, command.Scope, headID(owner, command.Scope), HeadEventType, previous, head, now); err != nil {
		return result, err
	}
	result.Receipt = Receipt{Command: command, AcceptedRevision: head.Revision, SnapshotID: definition.ID, AcceptedAt: now}
	if err := writeRecord(ctx, tx, command.Scope, commandID, ReceiptEventType, "", commandRecord{1, owner, result.Receipt}, now); err != nil {
		return result, err
	}
	result.Snapshot = readback(head)
	return result, nil
}

func readback(head Head) Readback {
	phase := "legacy"
	if head.Revision > 0 {
		phase = "prepared"
	}
	if head.Applied.SnapshotID != "" && head.Applied == head.Desired {
		phase = "applied"
	}
	return Readback{Schema: Schema, Scope: head.Scope, Branch: head.Branch, PreparedBranch: head.Branch, Revision: head.Revision, Desired: head.Desired, Applied: head.Applied, Phase: phase, PreparationError: head.PreparationError}
}

func ReadbackHead(head Head) Readback { return readback(head) }

func selectionFrom(definition Snapshot, revision int64) Selection {
	return Selection{AgentName: definition.AgentName, SelectionName: definition.SelectionName, AssistantID: definition.AssistantID,
		DisplayName: definition.DisplayName, IsDefault: definition.IsDefault, SnapshotID: definition.ID, Revision: revision}
}
