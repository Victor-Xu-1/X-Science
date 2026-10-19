package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

func delegatedRecordID(kind string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return "delegated:" + kind + ":" + hex.EncodeToString(digest[:])
}

func reservedDelegatedControlEvent(kind string) bool {
	switch kind {
	case DelegatedStopReceiptEventType, delegationAdmissionEventType, delegationOriginEventType, delegationFenceEventType,
		sessionDelegationPolicyEventType, sessionDelegationCommandEventType:
		return true
	}
	return false
}

func readDelegatedRecord(ctx context.Context, tx workspaceTransaction, frameID, id, kind string, destination any) (string, bool, error) {
	var actualFrame, actualKind, raw string
	err := tx.QueryRowContext(ctx, `SELECT frame_id,event_type,payload FROM frame_events WHERE id=?`, id).Scan(&actualFrame, &actualKind, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if actualFrame != frameID || actualKind != kind || decoder.Decode(destination) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", false, ErrDelegatedStopInvalid
	}
	return raw, true, nil
}

func writeDelegatedRecord(ctx context.Context, tx workspaceTransaction, frameID, id, kind, previous string, record any, now time.Time) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if previous != "" {
		changed, err := tx.ExecContext(ctx, `UPDATE frame_events SET payload=? WHERE id=? AND frame_id=? AND event_type=? AND payload=?`, string(raw), id, frameID, kind, previous)
		if err != nil {
			return err
		}
		if count, err := changed.RowsAffected(); err != nil || count != 1 {
			return ErrDelegatedStopRequestConflict
		}
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	_, err = appendKernelSupervisionFrameEvent(ctx, tx, FrameEventInput{ID: id, FrameID: frameID, Type: kind, Payload: payload}, now)
	return err
}

func delegatedScopeTx(ctx context.Context, tx workspaceTransaction, owner, frame string) (DelegatedStopScope, string, error) {
	var scope DelegatedStopScope
	var actualOwner, project string
	err := tx.QueryRowContext(ctx, `SELECT p.user_id,f.project_id,f.id,f.incarnation_id,root.id,root.incarnation_id
		FROM frames f JOIN projects p ON p.id=f.project_id
		JOIN frames root ON root.id=f.root_frame_id AND root.project_id=f.project_id WHERE f.id=?`, frame).Scan(
		&actualOwner, &project, &scope.ParentFrameID, &scope.ParentFrameIncarnationID, &scope.RootFrameID, &scope.RootFrameIncarnationID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && actualOwner != owner {
		return scope, "", ErrDelegatedStopUnavailable
	}
	if err != nil {
		return scope, "", err
	}
	if scope.ParentFrameIncarnationID == "" || scope.RootFrameIncarnationID == "" {
		return scope, "", ErrDelegatedStopScopeStale
	}
	return scope, project, nil
}

func delegatedStreamTx(ctx context.Context, tx workspaceTransaction, owner, frame string) (string, int64, int64, error) {
	var uid string
	var epoch, revision int64
	err := tx.QueryRowContext(ctx, `SELECT stream.stream_uid,stream.epoch,stream.input_revision
		FROM transcript_frame_authority authority JOIN transcript_streams stream
		ON stream.stream_uid=authority.active_stream_uid AND stream.epoch=authority.active_epoch
		AND stream.owner_id=authority.owner_id AND stream.session_id=authority.session_id
		WHERE authority.owner_id=? AND authority.session_id=? AND stream.frame_id=? AND stream.kind='frame_ref'
		AND authority.read_authority='transcript_payload_v1' AND authority.write_authority='transcript_payload_v1'`, owner, frame, frame).Scan(&uid, &epoch, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, 0, ErrDelegatedStopOriginUnavailable
	}
	return uid, epoch, revision, err
}

func delegationFenceTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, uid string, epoch int64, branch string) (delegationFenceRecord, string, string, error) {
	id := delegatedRecordID("branch", owner, scope.ParentFrameIncarnationID, uid, branch)
	expected := delegationFenceRecord{Version: 1, OwnerID: owner, Scope: scope, StreamUID: uid, StreamEpoch: epoch, BranchID: branch}
	record := expected
	raw, found, err := readDelegatedRecord(ctx, tx, scope.ParentFrameID, id, delegationFenceEventType, &record)
	if err != nil {
		return record, "", "", err
	}
	if found {
		generation := record.Generation
		record.Generation = 0
		if record != expected || generation < 0 {
			return record, "", "", ErrDelegatedStopScopeStale
		}
		record.Generation = generation
	}
	return record, id, raw, nil
}
