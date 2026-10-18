package workspace

import (
	"context"
	"database/sql"
	"encoding/json"

	transcriptstore "x-science/internal/persistence/transcript"
)

type ContextCompactionArchiveSource struct {
	OwnerID string
	Request transcriptstore.ContextCompactionRequest
}

func validateContextCompactionArchiveSourceTx(ctx context.Context, tx *sql.Tx, input CreateCompactionArchiveInput) error {
	source := input.CanonicalSource
	if source == nil || source.OwnerID == "" || source.Request.Validate() != nil || input.SourceEventID == nil || *input.SourceEventID <= 0 {
		return transcriptstore.ErrContextCompactionInvalid
	}
	target := source.Request.Expected
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT event.payload_json FROM frames frame JOIN projects project ON project.id=frame.project_id AND project.user_id=?
		JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
		JOIN transcript_streams stream ON stream.owner_id=project.user_id AND stream.frame_id=frame.id AND stream.stream_uid=? AND stream.epoch=?
		JOIN transcript_events event ON event.stream_uid=stream.stream_uid AND event.event_id=?
		JOIN transcript_branch_events member ON member.stream_uid=event.stream_uid AND member.branch_id=? AND member.event_id=event.event_id
		WHERE frame.id=? AND frame.incarnation_id=? AND frame.root_frame_id=? AND root.incarnation_id=? AND event.event_type='runner_checkpoint'`,
		source.OwnerID, target.StreamUID, target.StreamEpoch, *input.SourceEventID, target.BranchID, input.FrameID, target.FrameIncarnationID, target.RootFrameID, target.RootFrameIncarnationID).Scan(&raw)
	if err != nil {
		return err
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || !transcriptstore.IsCompactionBoundaryPayload(payload) {
		return transcriptstore.ErrContextCompactionConflict
	}
	encoded, err := json.Marshal(payload["maintenance"])
	var request transcriptstore.ContextCompactionRequest
	if err != nil || json.Unmarshal(encoded, &request) != nil || request != source.Request {
		return transcriptstore.ErrContextCompactionConflict
	}
	proof, err := transcriptstore.ReadContextCompactionBoundaryProof(ctx, tx, source.OwnerID, target.StreamUID, *input.SourceEventID)
	if err != nil {
		return err
	}
	if proof == nil {
		return transcriptstore.ErrContextCompactionConflict
	}
	return nil
}
