package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func contextCompactionTargetConn(ctx context.Context, conn *sql.Conn, owner, frame string) (ContextCompactionTarget, Stream, string, error) {
	var target ContextCompactionTarget
	var status, streamUID string
	err := conn.QueryRowContext(ctx, `SELECT f.incarnation_id,f.root_frame_id,root.incarnation_id,f.status,a.active_stream_uid
		FROM frames f JOIN projects p ON p.id=f.project_id AND p.user_id=?
		JOIN frames root ON root.id=f.root_frame_id AND root.project_id=f.project_id
		JOIN transcript_frame_authority a ON a.owner_id=p.user_id AND a.session_id=f.id
		WHERE f.id=? AND a.read_authority='transcript_payload_v1' AND a.write_authority='transcript_payload_v1'`, owner, frame).
		Scan(&target.FrameIncarnationID, &target.RootFrameID, &target.RootFrameIncarnationID, &status, &streamUID)
	if errors.Is(err, sql.ErrNoRows) {
		return target, Stream{}, status, ErrContextCompactionUnavailable
	}
	if err != nil {
		return target, Stream{}, status, err
	}
	stream, err := getStreamConn(ctx, conn, streamUID, owner)
	if err != nil {
		return target, stream, status, err
	}
	_, _, snapshot, err := projectionSnapshotForBranchTx(ctx, conn, streamUID, owner, "")
	if err != nil {
		return target, stream, status, err
	}
	target.StreamUID, target.StreamEpoch, target.InputRevision = stream.UID, stream.Epoch, stream.InputRevision
	target.BranchID, target.BranchGeneration, target.ThroughPublicationSeq = snapshot.BranchID, snapshot.BranchGeneration, snapshot.ThroughPublicationSequence
	return target, stream, status, nil
}

func sameContextCompactionIdentity(current, expected ContextCompactionTarget) bool {
	return current.FrameIncarnationID == expected.FrameIncarnationID && current.RootFrameID == expected.RootFrameID &&
		current.RootFrameIncarnationID == expected.RootFrameIncarnationID && current.StreamUID == expected.StreamUID && current.StreamEpoch == expected.StreamEpoch
}

func contextCompactionAvailabilityConn(ctx context.Context, conn *sql.Conn, stream Stream, status string) (ContextCompactionAvailability, error) {
	result := ContextCompactionAvailability{Available: true, Reason: "idle"}
	if err := CheckFrameConversationAvailable(ctx, conn, stream.FrameID); err != nil {
		if errors.Is(err, ErrConversationArchived) {
			return ContextCompactionAvailability{Reason: "archived"}, nil
		}
		return result, err
	}
	var running, queued bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts WHERE stream_uid=? AND status='running'),
		EXISTS(SELECT 1 FROM queued_user_messages WHERE frame_id=? AND state IN ('queued','delivering'))`, stream.UID, stream.FrameID).Scan(&running, &queued); err != nil {
		return result, err
	}
	switch {
	case status == "awaiting_user_response" || status == "awaiting_plan_approval":
		result = ContextCompactionAvailability{Reason: "waiting"}
	case running:
		result = ContextCompactionAvailability{Reason: "active"}
	case queued || stream.InputRevision > stream.ConsumedInputRevision:
		result = ContextCompactionAvailability{Reason: "queued"}
	}
	return result, nil
}

func (r *Repository) ContextCompactionSnapshot(ctx context.Context, owner, frame string) (ContextCompactionSnapshot, error) {
	result := ContextCompactionSnapshot{Schema: "x-science.context-compaction-snapshot.v1"}
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
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
	target, stream, status, err := contextCompactionTargetConn(ctx, conn, owner, frame)
	if err != nil {
		return result, err
	}
	result.Target = target
	result.Availability, err = contextCompactionAvailabilityConn(ctx, conn, stream, status)
	if err != nil {
		return result, err
	}
	// The latest admitted input is the only possible current maintenance owner.
	// Do not scan historical controls or resurrect an older operation as active.
	var raw []byte
	err = conn.QueryRowContext(ctx, `SELECT event.payload_json FROM transcript_input_admissions admission
		JOIN transcript_events event ON event.stream_uid=admission.stream_uid AND event.event_id=admission.event_id
		WHERE admission.stream_uid=? AND admission.source_kind='event' ORDER BY admission.input_revision DESC LIMIT 1`, stream.UID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		return result, err
	}
	request, err := ContextCompactionRequestFromPayload(payload)
	if err != nil || request == nil {
		return result, err
	}
	receipt, err := contextCompactionReceiptConn(ctx, conn, owner, frame, *request, r.now().UTC())
	if err == nil {
		result.Operation = &receipt
	}
	return result, err
}

func (r *Repository) ReadContextCompaction(ctx context.Context, owner, frame string, request ContextCompactionRequest) (ContextCompactionReceipt, error) {
	if r == nil || r.readDatabase() == nil {
		return ContextCompactionReceipt{}, ErrSchemaUnavailable
	}
	if err := request.Validate(); err != nil {
		return ContextCompactionReceipt{}, err
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return ContextCompactionReceipt{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return ContextCompactionReceipt{}, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
	return contextCompactionReceiptConn(ctx, conn, owner, frame, request, r.now().UTC())
}
