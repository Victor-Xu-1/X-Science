package memoryextract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	workspace "x-science/internal/persistence/workspace"
)

// extractionInputDigest binds the immutable completed snapshot, not mutable
// model/configuration defaults or newly recalled facts on a later retry.
func extractionInputDigest(input RunInput) (string, error) {
	return ExtractionSnapshotDigest(input.Messages, input.FinalResponseText)
}

// ExtractionSnapshotDigest is the original completed-input digest. Coverage
// reads use the same encoding without changing existing receipt identities.
func ExtractionSnapshotDigest(messages []Message, final string) (string, error) {
	raw, err := json.Marshal(struct {
		Messages []Message
		Final    string
	}{messages, final})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (r *Runner) readReplay(ctx context.Context, input RunInput) (RunResult, bool, error) {
	if input.Source == nil {
		return RunResult{}, false, nil
	}
	if !sourceMatchesScope(input.Source, input.Scope) {
		return RunResult{Gate: "skipped-source"}, true, workspace.ErrExtractionSourceChanged
	}
	saved, found, err := r.store.ReadMemoryExtractionReceipt(ctx, *input.Source)
	if err != nil || !found {
		return RunResult{Gate: "replay-failed"}, false, err
	}
	digest, err := extractionInputDigest(input)
	if err != nil {
		return RunResult{Gate: "replay-failed"}, true, err
	}
	if saved.InputSHA256 != digest {
		return RunResult{Gate: "replay-conflict"}, true, workspace.ErrExtractionPlanConflict
	}
	return RunResultFromCommit(saved), true, nil
}

func sourceMatchesScope(source *workspace.CompletedExtractionSource, scope ApplyScope) bool {
	return source != nil && source.OwnerUserID == scope.UserID &&
		source.ProjectID == scope.ProjectID && source.FrameID == scope.SourceFrameID
}

func (r *Runner) commit(ctx context.Context, input RunInput, config Config, result RunResult, plan workspace.ExtractionMutationPlan) (RunResult, error) {
	if !sourceMatchesScope(input.Source, input.Scope) ||
		(input.Source.Legacy != nil && input.CommitGuard == nil) {
		result.Gate = "skipped-source"
		return result, workspace.ErrExtractionSourceChanged
	}
	digest, err := extractionInputDigest(input)
	if err != nil {
		result.Gate = "commit-failed"
		return result, err
	}
	var saved workspace.ExtractionCommitResult
	commit := func(check workspace.LegacyExtractionOrderCheck) error {
		var commitErr error
		saved, commitErr = r.store.CommitMemoryExtraction(ctx, workspace.ExtractionCommitInput{
			DeliveryClaim:    input.DeliveryClaim,
			LegacyOrderCheck: check,
			Source:           *input.Source, InputSHA256: digest,
			CursorBefore: result.CursorBefore, CursorNext: len(input.Messages),
			AdvanceCursor: config.ExtractDelta, Overshoot: result.Overshoot,
			DeltaCount: result.DeltaCount, Gate: result.Gate, Plan: plan,
		})
		return commitErr
	}
	if input.CommitGuard != nil {
		err = input.CommitGuard(ctx, commit)
	} else {
		err = commit(nil)
	}
	if err != nil {
		// A competing identical completion, or a committed reply whose outcome
		// was uncertain, may already own the immutable receipt. Restore that
		// source/input-bound outcome; never regenerate a plan to "repair" it.
		if replayed, found, replayErr := r.readReplay(ctx, input); replayErr == nil && found {
			return replayed, nil
		}
		// Prepared operations are not committed effects. Never expose their IDs
		// or claim "done" when a cursor/receipt/source fence rolled back.
		result.Applied = plan.Prepared
		result.Applied.Appended, result.Applied.Replaced, result.Applied.Removed = nil, nil, nil
		result.Gate = "commit-failed"
		if errors.Is(err, workspace.ErrExtractionSourceChanged) {
			result.Gate = "skipped-source"
		}
		result.CursorError = err.Error()
		return result, err
	}
	return RunResultFromCommit(saved), nil
}

// RunResultFromCommit restores the original durable outcome without implying a
// new write or requiring a model request after process restart.
func RunResultFromCommit(saved workspace.ExtractionCommitResult) RunResult {
	return RunResult{
		DeliverySettled: saved.DeliverySettled,
		Gate:            saved.Gate, CursorBefore: saved.CursorBefore, CursorAfter: saved.CursorAfter,
		CursorAdvanced: saved.CursorAdvanced, Overshoot: saved.Overshoot,
		DeltaCount: saved.DeltaCount, Applied: saved.Applied, Replayed: saved.Replayed,
	}
}
