package server

import (
	"context"
	"errors"
)

const (
	sessionRunnerHistoryPreparationFailedReasonCode = "runner_history_preparation_failed"
	sessionRunnerPreparationFailedReasonCode        = "runner_preparation_failed"
)

// This private identity is attached only by the trusted preparation boundary.
// It preserves diagnostics and Is/As; it is not a bounded correction or grant.
type sessionRunnerPreparationFailure struct {
	stage string
	cause error
}

func (failure sessionRunnerPreparationFailure) Error() string { return failure.cause.Error() }
func (failure sessionRunnerPreparationFailure) Unwrap() error { return failure.cause }

func (failure sessionRunnerPreparationFailure) reasonCode() string {
	switch failure.stage {
	case "history_replay", "history_compaction", "context_replay":
		return sessionRunnerHistoryPreparationFailedReasonCode
	default:
		return sessionRunnerPreparationFailedReasonCode
	}
}

func classifySessionRunnerPreparationFailure(stage string, cause error) error {
	if cause == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) ||
		sessionRunnerErrorReasonCode(cause) != "runner_execution_failed" {
		return cause
	}
	return sessionRunnerPreparationFailure{stage: stage, cause: cause}
}
