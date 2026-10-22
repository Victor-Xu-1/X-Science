package transcript

import (
	"context"
	"database/sql"
)

// CompletedMemorySource is read-only provenance, not a runner mutation claim.
// Event and publication coordinates are deliberately distinct.
type CompletedMemorySource struct {
	StreamUID, OwnerID, FrameID, FrameIncarnationID string
	RootFrameID, RootFrameIncarnationID             string
	StreamEpoch                                     int64
	BranchID                                        string
	BranchGeneration                                int64
	EventID, PublicationSeq, Attempt, InputRevision int64
}

// ReadCompletedMemorySource accepts only the exact completed receipt in the
// currently selected frame history. A replacement branch never substitutes its
// own nearest terminal event for a delayed extraction request.
func (r *Repository) ReadCompletedMemorySource(ctx context.Context, streamUID, ownerID string, eventID int64) (CompletedMemorySource, error) {
	var source CompletedMemorySource
	if r == nil || r.readDatabase() == nil || eventID <= 0 {
		return source, ErrEventConflict
	}
	tx, err := r.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return source, err
	}
	defer tx.Rollback()
	source, err = readCompletedMemorySource(ctx, tx, streamUID, ownerID, eventID)
	if err != nil {
		return CompletedMemorySource{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompletedMemorySource{}, err
	}
	return source, nil
}

// ReadCompletedMemorySource uses the same completed-history query as the
// repository reader, but retains its proof inside the caller's write fence.
func (tx *ImmediateTransaction) ReadCompletedMemorySource(ctx context.Context, streamUID, ownerID string, eventID int64) (CompletedMemorySource, error) {
	if tx == nil || tx.conn == nil || eventID <= 0 {
		return CompletedMemorySource{}, ErrEventConflict
	}
	return readCompletedMemorySource(ctx, tx, streamUID, ownerID, eventID)
}

func readCompletedMemorySource(ctx context.Context, tx transcriptQueryer, streamUID, ownerID string, eventID int64) (CompletedMemorySource, error) {
	var source CompletedMemorySource
	_, _, snapshot, err := projectionSnapshotForBranchTx(ctx, tx, streamUID, ownerID, "")
	if err != nil {
		return source, err
	}
	source.StreamUID, source.OwnerID = streamUID, ownerID
	source.BranchID, source.BranchGeneration = snapshot.BranchID, snapshot.BranchGeneration
	err = tx.QueryRowContext(ctx, `SELECT frame.id,frame.incarnation_id,root.id,root.incarnation_id,stream.epoch,
	 event.event_id,event.publication_seq,event.runner_attempt,attempt.claimed_input_revision
	 FROM transcript_streams stream
	 JOIN transcript_frame_authority authority ON authority.owner_id=stream.owner_id AND authority.session_id=stream.session_id
	  AND authority.active_stream_uid=stream.stream_uid AND authority.active_epoch=stream.epoch
	 JOIN frames frame ON frame.id=stream.frame_id
	 JOIN projects project ON project.id=frame.project_id AND project.user_id=stream.owner_id
	 JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
	 JOIN transcript_branch_events member ON member.stream_uid=stream.stream_uid AND member.branch_id=?
	 JOIN transcript_events event ON event.stream_uid=member.stream_uid AND event.event_id=member.event_id
	 JOIN transcript_runner_receipts receipt ON receipt.stream_uid=event.stream_uid AND receipt.event_id=event.event_id
	 JOIN transcript_runner_attempts attempt ON attempt.stream_uid=receipt.stream_uid AND attempt.attempt=receipt.attempt
	 WHERE stream.stream_uid=? AND stream.owner_id=? AND event.event_id=?
	 AND stream.kind='frame_ref' AND stream.project_id=frame.project_id
	 AND event.event_type='runner_finished' AND event.source='payload'
	 AND event.runner_attempt=receipt.attempt AND receipt.status='completed'
	 AND attempt.status='completed' AND attempt.finished_event_id=event.event_id
	 AND json_extract(event.payload_json,'$.status')='completed'
	 AND NOT EXISTS (
	  SELECT 1 FROM transcript_input_admissions admission
	  JOIN transcript_events input_event ON input_event.stream_uid=admission.stream_uid AND input_event.event_id=admission.event_id
	  JOIN transcript_branch_events input_member ON input_member.stream_uid=input_event.stream_uid
	   AND input_member.event_id=input_event.event_id AND input_member.branch_id=member.branch_id
	  WHERE admission.stream_uid=stream.stream_uid AND admission.source_kind='event'
	   AND admission.input_revision>attempt.claimed_input_revision
	   AND input_event.publication_seq<=event.publication_seq
	 )`,
		snapshot.BranchID, streamUID, ownerID, eventID).Scan(&source.FrameID, &source.FrameIncarnationID,
		&source.RootFrameID, &source.RootFrameIncarnationID, &source.StreamEpoch,
		&source.EventID, &source.PublicationSeq, &source.Attempt, &source.InputRevision)
	if err != nil {
		return CompletedMemorySource{}, err
	}
	if source.PublicationSeq <= 0 || source.PublicationSeq > snapshot.ThroughPublicationSequence || source.Attempt <= 0 || source.InputRevision <= 0 {
		return CompletedMemorySource{}, ErrEventConflict
	}
	return source, nil
}
