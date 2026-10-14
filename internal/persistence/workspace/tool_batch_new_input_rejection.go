package workspace

import (
	"context"
	"encoding/json"

	"x-science/internal/kernelcontract"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Deferred proposals have no executable operation until their ordered start.
// Recovery must close an obsolete proposal before requesting a model response
// for newer composer input, rather than first admitting the old computation.
// This read does not create/alter execution or protocol state: the existing
// pre-execution rejection consumer publishes the original call's result once.
func deferredKernelProposalInputRejection(
	ctx context.Context, tx *transcriptstore.ImmediateTransaction,
	stream transcriptstore.Stream, claim transcriptstore.RunnerClaim,
	batch ToolCallBatch, item ToolCallBatchItem,
) (ToolCallPreExecutionRejection, error) {
	if stream.InputRevision != claim.ClaimedInputRevision {
		return ToolCallPreExecutionRejection{}, transcriptstore.ErrClaimStale
	}
	if batch.AdmittedInputRevision >= claim.ClaimedInputRevision ||
		item.State != ToolCallBatchItemStatePending || item.StartedEventID != 0 ||
		item.WaitingEventID != 0 || item.TerminalEventID != 0 ||
		!kernelcontract.Admitted(item.ToolName, item.ArgumentsJSON) {
		return ToolCallPreExecutionRejection{}, nil
	}
	_, deferred, err := loadKernelBatchAdmissionSource(ctx, tx, batch)
	if err != nil || !deferred {
		return ToolCallPreExecutionRejection{}, err
	}
	input, err := latestKernelRecoveryComposerInput(ctx, tx, stream)
	if err != nil || input.eventID == 0 || input.publication <= batch.SourcePublicationSeq {
		return ToolCallPreExecutionRejection{}, err
	}
	feedback, err := kernelRecoverySupersessionFeedback(input, claim.ClaimedInputRevision)
	if err != nil {
		return ToolCallPreExecutionRejection{}, err
	}
	// This consumer rejects a proposal; it is not the successful settlement of
	// an existing operation. Preserve the common factual metadata but obey the
	// Engine's strict pre-execution rejection contract (ok=false and code).
	var rejection map[string]json.RawMessage
	if err := json.Unmarshal(feedback, &rejection); err != nil {
		return ToolCallPreExecutionRejection{}, err
	}
	rejection["ok"] = json.RawMessage(`false`)
	rejection["code"] = json.RawMessage(`"superseded_by_new_user_input"`)
	feedback, err = json.Marshal(rejection)
	if err != nil {
		return ToolCallPreExecutionRejection{}, err
	}
	return ToolCallPreExecutionRejection{
		Rejected: true, FeedbackJSON: feedback, ToolCallID: item.ToolCallID, ToolName: item.ToolName,
	}, nil
}
