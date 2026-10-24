package memoryextract

import (
	"context"
	"reflect"

	workspace "x-science/internal/persistence/workspace"
)

// AdvanceExtractionCoverage folds one source-verified original receipt. The
// caller supplies snapshots in completed-source order, never commit order.
// A receipt can extend the prefix only if its examined interval has no gap.
func AdvanceExtractionCoverage(current []Message, covered int, prior []Message, final string, saved workspace.ExtractionCommitResult) (int, error) {
	if covered < 0 || covered > len(current) || saved.DeltaCount < 0 || saved.DeltaCount > len(prior) {
		return covered, workspace.ErrExtractionPlanConflict
	}
	if saved.Gate != "done" && saved.Gate != "agent-wrote" {
		return covered, nil
	}
	digest, err := ExtractionSnapshotDigest(prior, final)
	if err != nil {
		return covered, err
	}
	// A valid old outcome may use a projection this runtime cannot establish.
	// It stays immutable but does not grant coverage for a new completion.
	if digest != saved.InputSHA256 || len(prior) > len(current) || !reflect.DeepEqual(prior, current[:len(prior)]) {
		return covered, nil
	}
	start := len(prior) - saved.DeltaCount
	if start <= covered && len(prior) > covered {
		return len(prior), nil
	}
	return covered, nil
}

// A nonzero prefix may skip all model/commit work, so validate its host/source
// binding before no-delta can acknowledge it. Legacy uses the same policy and
// strict snapshot guard, without mutating the DB or retaining its checker.
func validateExtractionCoverageBinding(ctx context.Context, input RunInput, covered int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if covered == 0 {
		return nil
	}
	if !sourceMatchesScope(input.Source, input.Scope) {
		return workspace.ErrExtractionSourceChanged
	}
	if input.Source.Transcript != nil {
		return nil
	}
	if input.Source.Legacy == nil || input.CommitGuard == nil {
		return workspace.ErrExtractionPlanConflict
	}
	called := false
	err := input.CommitGuard(ctx, func(check workspace.LegacyExtractionOrderCheck) error {
		if called || check == nil {
			return workspace.ErrExtractionPlanConflict
		}
		called = true
		return nil
	})
	if err != nil {
		return err
	}
	if !called {
		return workspace.ErrExtractionPlanConflict
	}
	return nil
}
