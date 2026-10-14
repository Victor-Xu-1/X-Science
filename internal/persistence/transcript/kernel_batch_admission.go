package transcript

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"x-science/internal/kernelcontract"
)

const KernelBatchAdmissionVersion = 1

type RunnerKernelBatchAdmission struct {
	Version         int    `json:"version"`
	BatchID         string `json:"batchId"`
	Ordinal         int64  `json:"ordinal"`
	ProposalSHA256  string `json:"proposalSHA256"`
	InputSHA256     string `json:"inputSHA256"`
	InputByteLength int    `json:"inputByteLength"`
}

func runnerCheckpointContainsKernelAdmission(payload []byte) bool {
	var checkpoint struct {
		Admission json.RawMessage `json:"kernelAdmission"`
	}
	return json.Unmarshal(payload, &checkpoint) == nil && len(checkpoint.Admission) > 0
}

// validateOrderedKernelCheckpoint retains the legacy validator unchanged.
// New ordered proposals have no executable authority until the exact current
// item's start checkpoint atomically commits its canonical operation.
func validateOrderedKernelCheckpoint(ctx context.Context, conn *sql.Conn, event Event, payload []byte, receipt RunnerCheckpointCommitReceipt) (bool, error) {
	var checkpoint struct {
		Version        int                           `json:"kernelAdmissionVersion"`
		Admission      *RunnerKernelBatchAdmission   `json:"kernelAdmission"`
		ToolCallID     string                        `json:"toolCallId"`
		ToolName       string                        `json:"toolName"`
		ToolPhase      string                        `json:"toolPhase"`
		ModelToolCalls []runnerCheckpointDurableCall `json:"modelToolCalls"`
	}
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return true, err
	}
	if checkpoint.Version == 0 && checkpoint.Admission == nil {
		return false, nil
	}
	invalid := errors.New("ordered kernel admission receipt conflicts with its durable proposal")
	if checkpoint.Version != 0 {
		if checkpoint.Version != KernelBatchAdmissionVersion || checkpoint.Admission != nil || len(checkpoint.ModelToolCalls) < 2 || len(receipt.KernelOperations) != 0 {
			return true, invalid
		}
		if err := validateRunnerCheckpointToolBatchReceipt(ctx, conn, event, checkpoint.ModelToolCalls, receipt.ToolBatch); err != nil {
			return true, err
		}
		// Replay may observe already admitted operations. Every such operation
		// must still have its original immutable start witness, never a new input.
		var unwitnessed int
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_local_operations op
			LEFT JOIN transcript_tool_call_items item ON item.stream_uid=op.stream_uid AND item.tool_call_id=op.tool_call_id
			LEFT JOIN transcript_events start ON start.stream_uid=item.stream_uid AND start.event_id=item.started_event_id
			WHERE op.stream_uid=? AND op.source_event_id=? AND
			(start.event_id IS NULL OR json_extract(start.payload_json,'$.kernelAdmission.version') IS NOT 1
			 OR json_extract(start.payload_json,'$.kernelAdmission.batchId') IS NOT item.batch_id
			 OR json_extract(start.payload_json,'$.kernelAdmission.ordinal') IS NOT op.tool_call_ordinal
			 OR json_extract(start.payload_json,'$.kernelAdmission.proposalSHA256') IS NOT item.arguments_sha256
			 OR json_extract(start.payload_json,'$.kernelAdmission.inputSHA256') IS NOT op.input_sha256
			 OR json_extract(start.payload_json,'$.kernelAdmission.inputByteLength') IS NOT length(CAST(op.input_json AS BLOB)))`, event.StreamUID, event.EventID).Scan(&unwitnessed)
		if err != nil {
			return true, err
		}
		if unwitnessed != 0 {
			return true, invalid
		}
		return true, nil
	}
	a := checkpoint.Admission
	if a == nil || a.Version != KernelBatchAdmissionVersion || a.Ordinal < 0 || len(checkpoint.ModelToolCalls) != 0 || checkpoint.ToolPhase != "start" || receipt.ToolBatch != nil || len(receipt.KernelOperations) != 1 || event.RunnerAttempt == nil {
		return true, invalid
	}
	r := receipt.KernelOperations[0]
	var input []byte
	if err := conn.QueryRowContext(ctx, `SELECT input_json FROM kernel_local_operations WHERE operation_id=? AND stream_uid=?`, r.OperationID, event.StreamUID).Scan(&input); err != nil {
		return true, err
	}
	canonical, _, err := kernelcontract.CanonicalInput(checkpoint.ToolName, input)
	if err != nil || !bytes.Equal(canonical, input) {
		return true, invalid
	}
	digest := sha256.Sum256(canonical)
	if r.Ordinal != a.Ordinal || r.ToolCallID != checkpoint.ToolCallID || r.Tool != checkpoint.ToolName || r.InputSHA256 != hex.EncodeToString(digest[:]) || a.InputSHA256 != r.InputSHA256 || a.InputByteLength != len(canonical) || r.InitialStateVersion != 1 {
		return true, invalid
	}
	var matches int
	err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_tool_call_batches batch
		JOIN transcript_tool_call_items item ON item.batch_id=batch.batch_id AND item.ordinal=?
		JOIN transcript_events source ON source.stream_uid=batch.stream_uid AND source.event_id=batch.source_event_id
		JOIN kernel_local_operations op ON op.stream_uid=batch.stream_uid AND op.source_event_id=batch.source_event_id AND op.tool_call_ordinal=item.ordinal
		JOIN frame_events approval ON approval.id=? AND approval.frame_id=op.frame_id
		JOIN workspace_outbox outbox ON outbox.idempotency_key=?
		WHERE batch.batch_id=? AND batch.stream_uid=? AND batch.next_ordinal=item.ordinal
		 AND item.started_event_id=? AND item.state IN ('running','waiting')
		 AND batch.runner_attempt=? AND op.owner_user_id=batch.owner_user_id
		 AND item.tool_call_id=? AND item.tool_name=? AND item.arguments_sha256=?
		 AND json_extract(source.payload_json,'$.kernelAdmissionVersion')=1
		 AND op.operation_id=? AND op.input_json=? AND op.input_sha256=? AND op.approval_request_id=?
		 AND approval.event_type='kernel_local_exec_approval_requested'
		 AND json_extract(approval.payload,'$.operation_id')=op.operation_id
		 AND json_extract(approval.payload,'$.requestId')=op.approval_request_id`,
		a.Ordinal, r.RequestedEventID, "frame-event:"+r.RequestedEventID, a.BatchID, event.StreamUID, event.EventID, *event.RunnerAttempt,
		checkpoint.ToolCallID, checkpoint.ToolName, a.ProposalSHA256, r.OperationID, string(canonical), r.InputSHA256, r.ApprovalRequestID).Scan(&matches)
	if err != nil {
		return true, err
	}
	if matches != 1 {
		return true, invalid
	}
	return true, nil
}
