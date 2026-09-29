package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	eventjournal "synon-go/internal/persistence/journal"
	transcriptstore "synon-go/internal/persistence/transcript"
	"synon-go/internal/sessionrunner"
)

const providerGenerationRecoveryField = "provider_generation_recovery"

type providerGenerationRecovery struct {
	Version       int       `json:"version"`
	Scope         string    `json:"scope"`
	InputRevision int64     `json:"input_revision"`
	Consecutive   int       `json:"consecutive"`
	NotBefore     time.Time `json:"not_before,omitempty"`
}

type sessionOutputBudgetRecoveryError struct {
	cause   error
	scope   string
	canGrow bool
}

func (e *sessionOutputBudgetRecoveryError) Error() string { return e.cause.Error() }
func (e *sessionOutputBudgetRecoveryError) Unwrap() error { return e.cause }

func (run *sessionRunnerChatRun) generationRecoverySnapshot() providerGenerationRecovery {
	run.generationRecoveryMu.Lock()
	defer run.generationRecoveryMu.Unlock()
	return run.generationRecovery
}

func (run *sessionRunnerChatRun) resetGenerationRecoveryProgress() {
	if run == nil {
		return
	}
	run.generationRecoveryMu.Lock()
	defer run.generationRecoveryMu.Unlock()
	run.generationRecovery.Consecutive = 0
	run.generationRecovery.NotBefore = time.Time{}
}

// Restore facts from the active canonical branch, not its model-visible window.
// The existing bounded read cache stores only eligible receipts and observations.
func (s *Server) restoreRunnerProgressEntry(run *sessionRunnerChatRun, entry eventjournal.Entry) error {
	if run == nil {
		return nil
	}
	if runnerEntryStartsNewLogicalTask(entry) {
		run.readReuse = nil
		run.resetGenerationRecoveryProgress()
		return nil
	}
	message := entry.Message
	if raw, present := message[providerGenerationRecoveryField]; present {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		var state providerGenerationRecovery
		decoder := json.NewDecoder(strings.NewReader(string(encoded)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil {
			return fmt.Errorf("decode generation recovery: %w", err)
		}
		if state.Version != 1 || state.Scope == "" || state.InputRevision <= 0 || state.Consecutive < 0 || state.Consecutive > sessionrunner.MaxRecoveryStreak {
			return errors.New("invalid generation recovery checkpoint")
		}
		if run.Transcript != nil && state.InputRevision == run.Transcript.Claim.ClaimedInputRevision {
			run.generationRecoveryMu.Lock()
			run.generationRecovery = state
			run.generationRecoveryMu.Unlock()
		}
		return nil
	}
	if stringValue(message["toolPhase"]) != "completed" {
		return nil
	}
	input := decodeReadReuseMap(message["toolInput"])
	name := stringValue(message["toolName"])
	result, found := s.hydrateSessionRunnerReadReuseValue(run, name, stringValue(message["toolCallId"]), message["toolResult"])
	if !found {
		return nil
	}
	if name == "read_file" && !runnerCorrectionReadInput(name, input) {
		result = run.observeFileRead(input, result)
	} else {
		s.hydrateSessionRunnerReadReuse(run, []eventjournal.Entry{entry})
	}
	result = run.observeExecutionEvidence(name, result)
	if runnerGenerationToolProgress(name, result) {
		run.resetGenerationRecoveryProgress()
	}
	return nil
}

// Each failure is measured and committed under the live claim even when no
// private content or complete tool arguments exist. This closes the empty-output
// route as well as ordinary continuation; it does not create another scheduler.
func (s *Server) checkpointGenerationRecovery(ctx context.Context, run *sessionRunnerChatRun, cause error) (bool, error) {
	if run == nil || run.Transcript == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, sessionRunnerContentDeltaPersistenceTimeout)
	defer cancel()
	scope, canGrow := "provider-default", false
	var fixed *sessionOutputBudgetSaturatedError
	var adaptive *sessionOutputBudgetRecoveryError
	if errors.As(cause, &fixed) && fixed.scope != "" {
		scope = fixed.scope
	}
	if errors.As(cause, &adaptive) {
		scope, canGrow = adaptive.scope, adaptive.canGrow
	}
	state := run.generationRecoverySnapshot()
	if state.Scope != scope || state.InputRevision != run.Transcript.Claim.ClaimedInputRevision {
		state = providerGenerationRecovery{Version: 1, Scope: scope, InputRevision: run.Transcript.Claim.ClaimedInputRevision}
	}
	next, park := sessionrunner.AdvanceRecovery(sessionrunner.RecoveryProgress{
		SemanticBytes: run.ProviderAttemptSemanticBytes, BudgetCanGrow: canGrow, Consecutive: state.Consecutive,
	})
	state.Consecutive = min(next, sessionrunner.MaxRecoveryStreak)
	state.NotBefore = time.Time{}
	if state.Consecutive > 0 && !park {
		// The generic runner pool can reclaim an expired lease before the frame
		// dispatcher sees it. Preserve the same backoff deadline in canonical
		// recovery state so either entry path must wait before another generation.
		state.NotBefore = frameResumeDispatchAutoResumePolicy("provider_stream_no_progress", int64(state.Consecutive), run.Transcript.Stream.UID, time.Now().UTC())
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, run.Transcript.Stream.UID, run.Transcript.Stream.OwnerID)
	if err != nil {
		return false, err
	}
	event, err := s.checkpointTranscriptRunnerEvent(ctx, run.Transcript, transcriptstore.RunnerPhaseExecuting,
		fmt.Sprintf("generation-recovery-%d", snapshot.ThroughPublicationSequence), map[string]any{providerGenerationRecoveryField: state}, false)
	if err != nil {
		return false, err
	}
	run.AfterEventID = maxInt64(run.AfterEventID, event.EventID)
	run.generationRecoveryMu.Lock()
	run.generationRecovery = state
	run.generationRecoveryMu.Unlock()
	return park, nil
}

func (run *sessionRunnerChatRun) waitGenerationRecovery(ctx context.Context) error {
	if run == nil {
		return nil
	}
	delay := time.Until(run.generationRecoverySnapshot().NotBefore)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (run *sessionRunnerChatRun) generationRecoveryContext() string {
	if run == nil || run.generationRecoverySnapshot().Consecutive < 2 {
		return ""
	}
	return "Generation recovery: repeated generations have not completed a useful action. Reuse completed evidence and existing files. Produce one independently valid, bounded next action; divide a large edit or computation into smaller verified steps without reducing the requested deliverable. Do not repeat setup or evidence inspection unless relevant state changed. Never execute or repair a truncated tool argument by guessing missing bytes."
}
