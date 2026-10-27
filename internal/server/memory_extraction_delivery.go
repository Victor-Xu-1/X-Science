package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"x-science/internal/memoryextract"
	"x-science/internal/outbox"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

type memoryExtractionDeliverer struct{ server *Server }

func (deliverer memoryExtractionDeliverer) Deliver(ctx context.Context, event workspace.OutboxEvent) error {
	return deliverer.server.deliverMemoryExtraction(ctx, event)
}

// Uses the existing durable, renewable outbox coordinator and existing observer
// shutdown ownership. No per-process queue/counter is a recovery authority.
func (s *Server) startMemoryExtractionDelivery() {
	if s == nil || s.workspaceStore == nil || s.memoryExtraction == nil {
		return
	}
	const owner = "memory-extraction-dispatcher"
	ctx, cancel, acquired := s.reserveDetachedKernelObserver(context.Background(), owner)
	if !acquired {
		return
	}
	dispatcher, err := outbox.NewDispatcher(s.workspaceStore, memoryExtractionDeliverer{s}, outbox.Options{
		WorkerID: "memory-extraction-" + uuid.NewString(), Topics: []string{workspace.MemoryExtractionOutboxTopic}, BatchSize: 2,
		Lease: 30 * time.Second, LongRunning: true, RetryBase: time.Second, RetryMax: time.Minute,
		OnError: func(err error) { log.Printf("memory extraction delivery: %v", err) },
	})
	if err != nil {
		s.releaseDetachedKernelObserver(owner, cancel)
		log.Printf("start memory extraction delivery: %v", err)
		return
	}
	go func() { defer s.releaseDetachedKernelObserver(owner, cancel); _ = dispatcher.Run(ctx) }()
}

func (s *Server) settleMemoryDelivery(ctx context.Context, event workspace.OutboxEvent, diagnostic string) error {
	if err := s.workspaceStore.SettleMemoryExtractionDelivery(ctx, event, diagnostic); err != nil {
		return err
	}
	return outbox.ErrDeliverySettled
}

func (s *Server) deliverMemoryExtraction(ctx context.Context, event workspace.OutboxEvent) error {
	if s == nil || s.workspaceStore == nil || s.memoryExtraction == nil {
		return errors.New("memory extraction delivery is unavailable")
	}
	value, err := workspace.DecodeMemoryExtractionDispatch(event)
	if err != nil {
		return err
	}
	// A committed receipt is an original outcome, not a fresh model request.
	// Read it before changing configuration, request-view or selected-branch gates.
	_, committed, err := s.workspaceStore.ReadMemoryExtractionReceipt(ctx, value.Source)
	if err != nil {
		if errors.Is(err, workspace.ErrExtractionSourceChanged) {
			return s.settleMemoryDelivery(ctx, event, "completed-source-no-longer-owned")
		}
		return err
	}
	if committed {
		return s.settleMemoryDelivery(ctx, event, "")
	}
	if value.Completion.Diagnostic != "" {
		return s.settleMemoryDelivery(ctx, event, value.Completion.Diagnostic)
	}
	if err := s.workspaceStore.CheckMemoryExtractionDeliveryClaim(ctx, event); err != nil {
		return err
	}
	proof, err := s.transcriptStore.ReadCompletedMemorySource(ctx, value.Source.Transcript.StreamUID, value.Source.OwnerUserID, value.Source.Transcript.EventID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, transcriptstore.ErrEventConflict) || err == nil && proof != *value.Source.Transcript {
		return s.settleMemoryDelivery(ctx, event, "completed-source-no-longer-selected")
	}
	if err != nil {
		return err
	}
	claim := workspace.MemoryExtractionClaim(event)
	source := memoryExtractionSource{
		finishEventID: value.Source.Transcript.EventID, attempt: value.Source.Transcript.Attempt, proof: value.Source.Transcript,
		captured: true, deliveryClaim: &claim, extractMode: value.Completion.Mode, model: value.Completion.Model, cadence: &value.Cadence,
		modeDecision: value.Completion.ModeDecision.Clone(),
	}
	if value.Completion.Mode == "forked" {
		source.request, err = decodeMemoryForkedRequestView(value.Completion.RequestView)
		if err != nil {
			return s.settleMemoryDelivery(ctx, event, "original-request-view-invalid")
		}
	}
	result, err := s.memoryExtraction.runCompletedRoot(ctx, value.SessionID, source)
	if err != nil {
		if errors.Is(err, workspace.ErrExtractionSourceChanged) || errors.Is(err, workspace.ErrAgentMemoryMutationForbidden) {
			return s.settleMemoryDelivery(ctx, event, "completed-source-or-policy-changed")
		}
		return memoryExtractionDeliveryFailure(memoryextract.RunResult{Gate: result.Gate, ExtractorCause: err})
	}
	if result.DeliverySettled {
		return outbox.ErrDeliverySettled
	}
	if errors.Is(result.ExtractorCause, workspace.ErrAgentMemoryMutationForbidden) {
		return s.settleMemoryDelivery(ctx, event, "completed-source-or-policy-changed")
	}
	if result.Replayed {
		return s.settleMemoryDelivery(ctx, event, "")
	}
	if memoryExtractionFinalSkip(result) {
		return s.settleMemoryDelivery(ctx, event, "")
	}
	if result.Gate == "no-last-request-view" {
		return s.settleMemoryDelivery(ctx, event, "original-request-view-missing")
	}
	// Runner's extractor-failed / pi-unavailable are deliberately nil Go errors
	// so background work cannot fail the user turn. They are not delivery success.
	return memoryExtractionDeliveryFailure(result)
}

// Persist only closed, bounded presentation categories from existing typed
// causes/classifiers. Provider bodies, request text and headers stay in RAM.
func memoryExtractionDeliveryFailure(result memoryextract.RunResult) error {
	cause := result.ExtractorCause
	safeCause := "unavailable"
	if cause != nil {
		safeCause = sessionRunnerErrorReasonCode(cause)
		var syntax *json.SyntaxError
		var shape *json.UnmarshalTypeError
		switch {
		case errors.Is(cause, context.DeadlineExceeded):
			safeCause = "deadline_exceeded"
		case errors.Is(cause, context.Canceled):
			safeCause = "cancelled"
		case errors.As(cause, &syntax), errors.As(cause, &shape):
			safeCause = "invalid_json"
		default:
			if kind, ok := providers.HTTPFailureKind(cause); ok {
				safeCause = "provider_" + string(kind)
			}
		}
	} else if result.Gate == "pi-unavailable" {
		safeCause = "classifier_unavailable"
	}
	if !validRunnerFailureReason(safeCause) {
		safeCause = "runner_execution_failed"
	}
	return &memoryExtractionDeliveryError{gate: result.Gate, kind: safeCause, cause: cause}
}

type memoryExtractionDeliveryError struct {
	gate, kind string
	cause      error
}

func (failure *memoryExtractionDeliveryError) Error() string {
	return fmt.Sprintf("Memory extraction delivery did not commit: gate=%s cause=%s", failure.gate, failure.kind)
}

func (failure *memoryExtractionDeliveryError) Unwrap() error { return failure.cause }

func memoryExtractionFinalSkip(result memoryextract.RunResult) bool {
	switch result.Gate {
	case "disabled", "disabled-auto", "disabled-aside", "disabled-session", "not-root", "not-completed", "no-userId", "no-messages", "no-delta", "no-prose", "cadence":
		return true
	}
	return false
}
