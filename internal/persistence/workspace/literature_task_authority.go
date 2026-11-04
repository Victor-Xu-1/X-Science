package workspace

import (
	"context"
	"database/sql"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

var ErrLiteratureTaskAuthority = errors.New("Library task execution authority is stale or unavailable")

// This is an internal server-bound capability, not part of model input.
type LiteratureTaskAuthority struct {
	Access                  KernelFrameAccess
	Stream                  transcriptstore.Stream
	Claim                   transcriptstore.RunnerClaim
	ToolCallID              string
	KernelGeneration        uint64
	CurrentKernelGeneration func() uint64
}

// Logical scope is retained alongside the original request hash in the same
// mutation receipt. A lease renewal may read its receipt; a new input may not.
type LiteratureTaskScope struct {
	OwnerID                string `json:"owner_id"`
	ProjectID              string `json:"project_id"`
	FrameID                string `json:"frame_id"`
	FrameIncarnationID     string `json:"frame_incarnation_id"`
	RootFrameID            string `json:"root_frame_id"`
	RootFrameIncarnationID string `json:"root_frame_incarnation_id"`
	StreamUID              string `json:"stream_uid"`
	StreamEpoch            int64  `json:"stream_epoch"`
	InputRevision          int64  `json:"input_revision"`
}

func literatureTaskScopeTx(ctx context.Context, tx workspaceTransaction, authority LiteratureTaskAuthority) (LiteratureTaskScope, error) {
	a, stream, claim := authority.Access, authority.Stream, authority.Claim
	if !validLiteratureID(a.UserID) || !validLiteratureID(a.Frame.ID) || !validLiteratureID(a.Frame.IncarnationID) ||
		!validLiteratureID(a.Frame.RootFrameID) || !validLiteratureID(a.RootFrameIncarnationID) ||
		stream.UID == "" || stream.Epoch < 1 || stream.FrameID != a.Frame.ID || stream.RootFrameID != a.Frame.RootFrameID ||
		stream.OwnerID != a.UserID || stream.ProjectID != a.Frame.ProjectID ||
		claim.StreamUID != stream.UID || claim.OwnerID != a.UserID || claim.ClaimedInputRevision < 1 {
		return LiteratureTaskScope{}, ErrLiteratureTaskAuthority
	}
	scope := LiteratureTaskScope{OwnerID: a.UserID, ProjectID: a.Frame.ProjectID, FrameID: a.Frame.ID,
		FrameIncarnationID: a.Frame.IncarnationID, RootFrameID: a.Frame.RootFrameID, RootFrameIncarnationID: a.RootFrameIncarnationID,
		StreamUID: stream.UID, StreamEpoch: stream.Epoch, InputRevision: claim.ClaimedInputRevision}
	var valid int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM frames f
 JOIN projects p ON p.id=f.project_id AND p.user_id=?
 JOIN frames root ON root.id=f.root_frame_id AND root.project_id=f.project_id
	 JOIN transcript_streams stream ON stream.stream_uid=? AND stream.owner_id=p.user_id AND stream.frame_id=f.id
	 AND stream.project_id=f.project_id AND stream.root_frame_id=f.root_frame_id
 WHERE f.id=? AND f.incarnation_id=? AND f.root_frame_id=? AND root.incarnation_id=? AND f.project_id=? AND stream.epoch=?`,
		scope.OwnerID, scope.StreamUID, scope.FrameID, scope.FrameIncarnationID, scope.RootFrameID, scope.RootFrameIncarnationID, scope.ProjectID, scope.StreamEpoch).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, ErrLiteratureTaskAuthority
	}
	return scope, err
}

func validateLiteratureTaskAdmissionTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, authority LiteratureTaskAuthority, scope LiteratureTaskScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if authority.ToolCallID == "" || authority.KernelGeneration == 0 && authority.CurrentKernelGeneration != nil ||
		authority.KernelGeneration != 0 && (authority.CurrentKernelGeneration == nil || authority.CurrentKernelGeneration() != authority.KernelGeneration) {
		return ErrLiteratureTaskAuthority
	}
	stream, err := tx.ValidateLiveRunnerClaim(ctx, authority.Claim)
	if err != nil {
		return err
	}
	if stream.UID != scope.StreamUID || stream.Epoch != scope.StreamEpoch || stream.OwnerID != scope.OwnerID ||
		stream.ProjectID != scope.ProjectID || stream.FrameID != scope.FrameID || stream.RootFrameID != scope.RootFrameID {
		return ErrLiteratureTaskAuthority
	}
	var attempt, eventID int64
	err = tx.QueryRowContext(ctx, `SELECT event.runner_attempt,item.started_event_id
 FROM transcript_tool_call_items item JOIN transcript_tool_call_batches batch ON batch.batch_id=item.batch_id
 JOIN transcript_branch_state branch ON branch.stream_uid=batch.stream_uid AND branch.active_branch_id=batch.branch_id AND branch.generation=batch.branch_generation
 JOIN transcript_events event ON event.stream_uid=item.stream_uid AND event.event_id=item.started_event_id
 JOIN transcript_branch_events member ON member.stream_uid=item.stream_uid AND member.branch_id=branch.active_branch_id AND member.event_id=event.event_id
 WHERE item.stream_uid=? AND batch.owner_user_id=? AND item.tool_call_id=? AND item.ordinal=batch.next_ordinal AND item.state IN ('running','waiting')`,
		scope.StreamUID, scope.OwnerID, authority.ToolCallID).Scan(&attempt, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLiteratureTaskAuthority
	}
	if err != nil {
		return err
	}
	if attempt != authority.Claim.Attempt || eventID <= 0 {
		return ErrLiteratureTaskAuthority
	}
	return nil
}
