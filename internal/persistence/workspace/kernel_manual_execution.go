package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) AdmitManualKernelExecution(ctx context.Context, input AdmitManualKernelExecutionInput) (ManualKernelExecution, bool, error) {
	if s == nil || s.db == nil {
		return ManualKernelExecution{}, false, ErrWorkspaceStoreClosed
	}
	digest, err := manualKernelRequestDigest(input.Request)
	if err != nil {
		return ManualKernelExecution{}, false, err
	}
	record := ManualKernelExecution{Version: 1, Origin: "user", ManualKernelBinding: input.Binding, Request: input.Request, RequestSHA256: digest, State: "queued", StateVersion: 1, AcceptedAt: s.now().UTC()}
	if err := validateManualKernelExecution(record); err != nil {
		return ManualKernelExecution{}, false, err
	}
	created := false
	err = transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if err := validateManualKernelScope(ctx, tx, input.Binding); err != nil {
			return err
		}
		existing, _, found, err := readManualKernelExecution(ctx, tx, input.Binding.ExecutionID)
		if err != nil {
			return err
		}
		if found {
			if existing.ManualKernelBinding != input.Binding || existing.RequestSHA256 != digest {
				return ErrManualKernelExecutionConflict
			}
			record = existing
			return nil
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		var payload map[string]any
		if err = json.Unmarshal(raw, &payload); err != nil {
			return err
		}
		_, err = s.appendFrameOutboxEventTransaction(ctx, tx, FrameEventInput{ID: manualKernelEventID(record.ExecutionID), FrameID: record.FrameID, Type: ManualKernelExecutionEventType, Payload: payload})
		created = err == nil
		return err
	})
	return record, created && err == nil, err
}

// Lookup validates the complete frozen request without selecting a new kernel.
func (s *Store) LookupManualKernelExecution(ctx context.Context, access KernelFrameAccess, request ManualKernelRequest) (ManualKernelExecution, bool, error) {
	digest, err := manualKernelRequestDigest(request)
	if err != nil {
		return ManualKernelExecution{}, false, err
	}
	id := ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID)
	record, found, err := s.GetManualKernelExecution(ctx, access, id)
	if err == nil && found && record.RequestSHA256 != digest {
		return ManualKernelExecution{}, false, ErrManualKernelExecutionConflict
	}
	return record, found, err
}

func (s *Store) GetManualKernelExecution(ctx context.Context, access KernelFrameAccess, executionID string) (ManualKernelExecution, bool, error) {
	if s == nil || s.db == nil {
		return ManualKernelExecution{}, false, ErrWorkspaceStoreClosed
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManualKernelExecution{}, false, err
	}
	defer tx.Rollback()
	binding := manualKernelAccessBinding(access)
	if err := validateManualKernelScope(ctx, tx, binding); err != nil {
		return ManualKernelExecution{}, false, err
	}
	record, _, found, err := readManualKernelExecution(ctx, tx, executionID)
	if err != nil {
		return ManualKernelExecution{}, false, err
	}
	if found && !manualKernelAccessMatches(record.ManualKernelBinding, binding) {
		return ManualKernelExecution{}, false, ErrManualKernelExecutionConflict
	}
	if err := tx.Commit(); err != nil {
		return ManualKernelExecution{}, false, err
	}
	return record, found, nil
}

func manualKernelAccessBinding(access KernelFrameAccess) ManualKernelBinding {
	return ManualKernelBinding{OwnerID: access.UserID, ProjectID: access.Frame.ProjectID, RootFrameID: access.Frame.RootFrameID, RootIncarnationID: access.RootFrameIncarnationID, FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID}
}

func manualKernelAccessMatches(a, b ManualKernelBinding) bool {
	return a.OwnerID == b.OwnerID && a.ProjectID == b.ProjectID && a.RootFrameID == b.RootFrameID && a.RootIncarnationID == b.RootIncarnationID && a.FrameID == b.FrameID && a.FrameIncarnationID == b.FrameIncarnationID
}

func validateManualKernelScope(ctx context.Context, tx workspaceTransaction, binding ManualKernelBinding) error {
	var actual ManualKernelBinding
	err := tx.QueryRowContext(ctx, `SELECT project.user_id,frame.project_id,frame.id,frame.incarnation_id,root.id,root.incarnation_id
		FROM frames frame JOIN projects project ON project.id=frame.project_id
		JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id WHERE frame.id=?`, binding.FrameID).Scan(
		&actual.OwnerID, &actual.ProjectID, &actual.FrameID, &actual.FrameIncarnationID, &actual.RootFrameID, &actual.RootIncarnationID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && actual.OwnerID != binding.OwnerID {
		return transcriptstore.ErrOwnerMismatch
	}
	if err != nil {
		return err
	}
	if !manualKernelAccessMatches(actual, binding) {
		return ErrManualKernelExecutionConflict
	}
	return nil
}

func readManualKernelExecution(ctx context.Context, tx workspaceTransaction, executionID string) (ManualKernelExecution, string, bool, error) {
	var frameID, eventType, raw string
	err := tx.QueryRowContext(ctx, `SELECT frame_id,event_type,payload FROM frame_events WHERE id=?`, manualKernelEventID(executionID)).Scan(&frameID, &eventType, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ManualKernelExecution{}, "", false, nil
	}
	if err != nil {
		return ManualKernelExecution{}, "", false, err
	}
	var record ManualKernelExecution
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || eventType != ManualKernelExecutionEventType || record.FrameID != frameID || record.ExecutionID != executionID {
		return ManualKernelExecution{}, "", false, ErrManualKernelExecutionInvalid
	}
	if err := validateManualKernelExecution(record); err != nil {
		return ManualKernelExecution{}, "", false, err
	}
	return record, raw, true, nil
}

func updateManualKernelExecution(ctx context.Context, tx workspaceTransaction, previous ManualKernelExecution, raw string, next ManualKernelExecution) error {
	if next.ManualKernelBinding != previous.ManualKernelBinding || next.RequestSHA256 != previous.RequestSHA256 || next.Request != previous.Request || !next.AcceptedAt.Equal(previous.AcceptedAt) {
		return ErrManualKernelExecutionConflict
	}
	next.StateVersion = previous.StateVersion + 1
	if err := validateManualKernelExecution(next); err != nil {
		return err
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE frame_events SET payload=? WHERE id=? AND frame_id=? AND event_type=? AND payload=?`, string(encoded), manualKernelEventID(previous.ExecutionID), previous.FrameID, ManualKernelExecutionEventType, raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrManualKernelExecutionStale
	}
	return nil
}
