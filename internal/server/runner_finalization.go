package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const sessionRunnerFinalMemoryPreparationTimeout = 500 * time.Millisecond

type transcriptRunnerFinalization struct {
	Content, Status, Detail, ReasonCode string
	AssistantIdentity                   string
	SegmentOrdinal                      int64
	PrepareMemory                       bool
	ResponseLanguage                    string
}

// This is only the final DB stage. Normal settlement and completion-only
// recovery keep their own locks, lifecycle hooks, cleanup and cancellation
// decisions; resolving a COMMIT never reruns those operations.
func (s *Server) persistTranscriptRunnerFinalization(parent context.Context, authority *transcriptRunnerAuthority, final transcriptRunnerFinalization) (transcriptstore.RunnerFinalizationReceipt, error) {
	if authority == nil || s.transcriptStore == nil {
		return transcriptstore.RunnerFinalizationReceipt{}, errors.New("canonical finalization requires transcript authority")
	}
	final.SegmentOrdinal = max(final.SegmentOrdinal, 1)
	final.Status, final.Detail, final.ReasonCode = strings.TrimSpace(final.Status), strings.TrimSpace(final.Detail), strings.TrimSpace(final.ReasonCode)
	terminalPayload := map[string]any{"status": final.Status, "detail": final.Detail,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(final.SegmentOrdinal, "")}
	if final.ReasonCode != "" {
		terminalPayload["reason_code"] = final.ReasonCode
	}
	raw, err := json.Marshal(terminalPayload)
	if err != nil {
		return transcriptstore.RunnerFinalizationReceipt{}, err
	}
	input := transcriptstore.RunnerFinalizationInput{Finish: transcriptstore.FinishRunnerInput{
		Claim: authority.Claim, ClientMessageID: transcriptRunnerClientMessageID(authority.Claim, "runner-finished"),
		Status: final.Status, PayloadJSON: raw, Destinations: transcriptRunnerDestinations(authority),
	}}
	if final.Content != "" {
		if final.AssistantIdentity == "" {
			final.AssistantIdentity = "assistant-message"
		}
		payload, err := json.Marshal(map[string]any{"text": final.Content,
			"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(final.SegmentOrdinal, "")})
		if err != nil {
			return transcriptstore.RunnerFinalizationReceipt{}, err
		}
		input.Assistant = &transcriptstore.AppendEventInput{Claim: authority.Claim,
			ClientMessageID: transcriptRunnerClientMessageID(authority.Claim, final.AssistantIdentity),
			Type:            "assistant_message", Source: transcriptstore.EventSourcePayload, PayloadJSON: payload, Destinations: transcriptRunnerDestinations(authority)}
	}
	// Optional reads do not consume the mandatory database window. Enqueue
	// still happens in that one atomic transaction, using the original policy.
	memory := s.prepareTranscriptFinalMemory(parent, authority, final)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), sessionRunnerContentDeltaPersistenceTimeout)
	defer cancel()
	receipt, outcome, err := s.persistTranscriptFinalizationInput(ctx, input, memory)
	var rejected *transcriptstore.RunnerFinalizationAssistantPayloadError
	if err != nil && !outcome.Transaction.CommitAttempted && errors.As(err, &rejected) {
		// Preserve the original deterministic append-rejection terminal. This
		// is not a DB retry or unknown-ACK resolution: no COMMIT was attempted,
		// the rejected assistant is removed, and the same remaining DB budget
		// is used for one real failed terminal with no automatic Memory work.
		const reason = "runner_response_persistence_failed"
		detail := publicSessionRunnerFailureMessage(fmt.Sprintf("append transcript assistant message failed: %v", rejected), final.ResponseLanguage, reason)
		failedPayload, marshalErr := json.Marshal(map[string]any{"status": "failed", "detail": detail, "reason_code": reason,
			"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(final.SegmentOrdinal, "")})
		if marshalErr != nil {
			return transcriptstore.RunnerFinalizationReceipt{}, marshalErr
		}
		input.Assistant = nil
		input.Finish.Status, input.Finish.PayloadJSON = "failed", failedPayload
		receipt, outcome, err = s.persistTranscriptFinalizationInput(ctx, input, nil)
	}
	if err != nil {
		return transcriptstore.RunnerFinalizationReceipt{}, fmt.Errorf("persist canonical finalization (commit_attempted=%t): %w", outcome.Transaction.CommitAttempted, err)
	}
	if !outcome.KnownCommit() {
		return transcriptstore.RunnerFinalizationReceipt{}, errors.New("canonical finalization has no confirmed commit")
	}
	if s.workspaceStore != nil {
		s.workspaceStore.NotifyKernelRunnerStateChanged()
	}
	if len(transcriptRunnerDestinations(authority)) > 0 {
		s.signalTranscriptWebDelivery()
	}
	return receipt, nil
}

func (s *Server) prepareTranscriptFinalMemory(parent context.Context, authority *transcriptRunnerAuthority, final transcriptRunnerFinalization) *workspace.MemoryExtractionCompletion {
	if !final.PrepareMemory || final.Status != "completed" || final.Content == "" || s.memoryExtraction == nil || s.workspaceStore == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), sessionRunnerFinalMemoryPreparationTimeout)
	defer cancel()
	completion, err := s.memoryExtraction.prepareCompletedMemoryExtraction(ctx, authority)
	if err != nil {
		return memoryExtractionPreparationDiagnostic(s.memoryConfig.ExtractMode, s.memoryConfig.ExtractEveryN)
	}
	return completion
}

func (s *Server) persistTranscriptFinalizationInput(ctx context.Context, input transcriptstore.RunnerFinalizationInput,
	memory *workspace.MemoryExtractionCompletion,
) (transcriptstore.RunnerFinalizationReceipt, transcriptstore.RunnerFinalizationOutcome, error) {
	if s.workspaceStore != nil {
		result, outcome, err := s.workspaceStore.FinalizeRunnerTranscript(ctx, workspace.RunnerFinalizationInput{Transcript: input, Memory: memory})
		return result.Transcript, outcome, err
	}
	return s.transcriptStore.FinalizeRunner(ctx, input)
}
