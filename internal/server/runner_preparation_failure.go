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

func (failure sessionRunnerPreparationFailure) diagnosticStage() string {
	switch failure.stage {
	case "history_replay", "history_compaction", "context_replay", "tool_discovery",
		"tool_arguments", "tool_rejection", "tool_checkpoint_authority", "tool_checkpoint_persistence":
		return failure.stage
	default:
		return "preparation"
	}
}

func (failure sessionRunnerPreparationFailure) reasonCode() string {
	switch failure.stage {
	case "history_replay", "history_compaction", "context_replay":
		return sessionRunnerHistoryPreparationFailedReasonCode
	case "tool_arguments":
		return "runner_tool_arguments_preparation_failed"
	case "tool_rejection":
		return "runner_tool_rejection_preparation_failed"
	case "tool_checkpoint_authority":
		return "runner_tool_checkpoint_authority_failed"
	case "tool_checkpoint_persistence":
		return "runner_tool_checkpoint_persistence_failed"
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
