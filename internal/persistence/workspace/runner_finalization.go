package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

type RunnerFinalizationInput struct {
	Transcript transcriptstore.RunnerFinalizationInput
	Memory     *MemoryExtractionCompletion
}

type RunnerFinalizationAside struct {
	FrameID string
	Payload json.RawMessage
}

// Effect fields describe only new writes in this operation. Replaying an old
// receipt must not overwrite later Aside output or enqueue Memory again.
type RunnerFinalizationResult struct {
	Transcript     transcriptstore.RunnerFinalizationReceipt
	EnqueuedMemory *OutboxEvent `json:"-"`
	Aside          *RunnerFinalizationAside
	Replayed       bool
}

type runnerFinalizationRun func(context.Context, func(*transcriptstore.ImmediateTransaction) error) (transcriptstore.ImmediateTransactionOutcome, error)

// FinalizeRunnerTranscript has one COMMIT for the canonical final answer,
// immutable references/commit ledger, terminal, optional Memory enqueue and
// applicable Aside projection. It never retries model/tools or a mutation to
// resolve an unknown COMMIT acknowledgement.
func (s *Store) FinalizeRunnerTranscript(ctx context.Context, input RunnerFinalizationInput) (RunnerFinalizationResult, transcriptstore.RunnerFinalizationOutcome, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		return RunnerFinalizationResult{}, transcriptstore.RunnerFinalizationOutcome{}, errors.New("final transcript persistence requires a bounded context")
	}
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return RunnerFinalizationResult{}, transcriptstore.RunnerFinalizationOutcome{}, err
	}
	return s.finalizeRunnerTranscriptWithRunner(ctx, input, repository, repository.RunImmediateWithOutcome)
}

func (s *Store) finalizeRunnerTranscriptWithRunner(ctx context.Context, input RunnerFinalizationInput, repository *transcriptstore.Repository, run runnerFinalizationRun) (RunnerFinalizationResult, transcriptstore.RunnerFinalizationOutcome, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		return RunnerFinalizationResult{}, transcriptstore.RunnerFinalizationOutcome{}, errors.New("final transcript persistence requires a bounded context")
	}
	if err := transcriptstore.ValidateRunnerFinalizationInput(input.Transcript); err != nil {
		return RunnerFinalizationResult{}, transcriptstore.RunnerFinalizationOutcome{}, err
	}
	asidePayload, err := prepareFinalizationAside(input.Transcript)
	if err != nil {
		return RunnerFinalizationResult{}, transcriptstore.RunnerFinalizationOutcome{}, err
	}
	var result RunnerFinalizationResult
	transaction, err := run(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		receipt, created, err := tx.FinalizeRunner(ctx, input.Transcript)
		if err != nil {
			return err
		}
		result.Transcript, result.Replayed = receipt, !created
		if input.Memory != nil {
			result.EnqueuedMemory, err = s.enqueueCompletedRunnerMemoryTx(ctx, tx, input.Transcript.Finish, receipt.Terminal, created, *input.Memory)
			if err != nil {
				return err
			}
		}
		if created && len(asidePayload) > 0 && receipt.Stream.FrameID != "" {
			frame, found, err := queryTraceFrame(ctx, tx, receipt.Stream.FrameID)
			if err != nil {
				return err
			}
			if found && strings.HasPrefix(frame.Name, "Aside") {
				if err := setFrameOutputDataTransaction(ctx, tx, frame.ID, asidePayload); err != nil {
					return err
				}
				result.Aside = &RunnerFinalizationAside{FrameID: frame.ID, Payload: append([]byte(nil), asidePayload...)}
			}
		}
		return nil
	})
	outcome := transcriptstore.RunnerFinalizationOutcome{Transaction: transaction}
	if err == nil {
		return result, outcome, nil
	}
	if !transaction.CommitAttempted {
		// This callback did not reach COMMIT. Do not expose rolled-back IDs as
		// accepted results; external tool/artifact resources remain untouched.
		return RunnerFinalizationResult{}, outcome, err
	}
	confirmed, found, readErr := s.confirmRunnerTranscriptFinalization(ctx, input, result, repository)
	if readErr == nil && found {
		outcome.ReadbackConfirmed = true
		return confirmed, outcome, nil
	}
	return result, outcome, fmt.Errorf("final transcript commit acknowledgement remains unknown: %w", errors.Join(err, readErr))
}

func prepareFinalizationAside(input transcriptstore.RunnerFinalizationInput) ([]byte, error) {
	if input.Assistant == nil || input.Finish.Status != "completed" {
		return nil, nil
	}
	if input.Assistant.Source != transcriptstore.EventSourcePayload {
		return nil, transcriptstore.ErrEventConflict
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(input.Assistant.PayloadJSON, &payload); err != nil {
		return nil, err
	}
	if payload.Text == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{"response": payload.Text})
}
