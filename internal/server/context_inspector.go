package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Calls here mean semantic main-model invocations, matching the recorder's
// RequestID. Provider HTTP retries/redirects are deliberately not counted.
type contextInspectionCall struct {
	ID             string                   `json:"id"`
	InputMessageID string                   `json:"inputMessageId,omitempty"`
	InputEventID   int64                    `json:"inputEventId,omitempty"`
	InputRevision  int64                    `json:"inputRevision,omitempty"`
	RunnerAttempt  int                      `json:"runnerAttempt"`
	Model          string                   `json:"model"`
	State          string                   `json:"state"`
	ObservedAt     time.Time                `json:"observedAt"`
	UsedTokens     int                      `json:"usedTokens"`
	LimitTokens    int                      `json:"limitTokens"`
	LimitSource    string                   `json:"limitSource"`
	OutputTokens   int                      `json:"outputTokens"`
	CallUsage      *agentruntime.ModelUsage `json:"callUsage,omitempty"`
}

type contextInspectionTurn struct {
	ID              string                     `json:"id"`
	InputMessageID  string                     `json:"inputMessageId"`
	InputRevision   int64                      `json:"inputRevision"`
	Status          string                     `json:"status"`
	TerminalEventID int64                      `json:"terminalEventId,omitempty"`
	CompletedAt     *time.Time                 `json:"completedAt,omitempty"`
	RunnerAttempts  []int64                    `json:"runnerAttempts"`
	CallIDs         []string                   `json:"callIds"`
	Sample          *runnerContextUsage        `json:"sample,omitempty"`
	Outcomes        []contextInspectionOutcome `json:"outcomes"`
}

type contextInspectionCoverage struct {
	Association       string `json:"association"`
	RetainedCallCount int    `json:"retainedCallCount"`
	// Observations include preparation; they are not a call or billing total.
	ObservedSampleCount int `json:"observedSampleCount"`
	// Exact session-wide semantic calls are known only with fully typed,
	// completely retained history. Missing means unknown, never zero.
	ObservedCallCount     *int `json:"observedCallCount,omitempty"`
	UnassociatedCallCount int  `json:"unassociatedCallCount"`
	Truncated             bool `json:"truncated"`
}

type contextInspection struct {
	Version  int                                    `json:"version"`
	Scope    transcriptstore.ContextInspectionScope `json:"scope"`
	Coverage contextInspectionCoverage              `json:"coverage"`
	Calls    []contextInspectionCall                `json:"calls"`
	Turns    []contextInspectionTurn                `json:"turns"`
}

func (s *Server) newClaimedContextUsageRecorder(ctx context.Context, sessionID string, run *sessionRunnerChatRun, options SessionRunnerChatOptions) *sessionContextUsageRecorder {
	recorder := newSessionContextUsageRecorder(s, sessionID, sessionRunnerAttempt(run), options)
	if recorder == nil || s.transcriptStore == nil || run == nil || run.Transcript == nil {
		return recorder
	}
	input, err := s.transcriptStore.ClaimedRunnerInputContext(ctx, run.Transcript.Claim)
	if err != nil {
		// Optional metrics cannot reject or retry the real model invocation.
		log.Printf("context_inspection_association_unavailable")
		return recorder
	}
	if input != nil && validContextAssociation(input.Association) && input.Association.Scope.ConversationID == sessionID {
		association := *input.Association
		recorder.association = &association
		recorder.attempt = int(association.RunnerAttempt)
	}
	return recorder
}

func validContextAssociation(value *transcriptstore.ContextCallAssociation) bool {
	if value == nil || value.Version != 1 || value.InputMessageID == "" || value.AdmissionBranchID == "" ||
		value.Scope.ConversationID == "" || value.Scope.FrameIncarnationID == "" || value.Scope.StreamUID == "" || value.Scope.BranchID == "" {
		return false
	}
	for _, number := range []int64{value.InputEventID, value.InputRevision, value.InputPublicationSequence,
		value.AdmissionBranchGeneration, value.ClaimedInputRevision, value.RunnerAttempt, value.Scope.StreamEpoch,
		value.Scope.BranchGeneration, value.Scope.ThroughPublicationSequence} {
		if number <= 0 || number > contextHistorySafeInteger {
			return false
		}
	}
	return value.InputRevision <= value.ClaimedInputRevision && value.InputPublicationSequence <= value.Scope.ThroughPublicationSequence &&
		value.AdmissionBranchGeneration <= value.Scope.BranchGeneration
}

func (s *Server) contextInspectionAuthority(ctx context.Context, sessionID string) (*transcriptstore.ContextInspectionSnapshot, string, error) {
	if s.transcriptStore == nil {
		return nil, "", nil
	}
	stream, authoritative, err := s.resolveTranscriptFrameStream(ctx, sessionID)
	if err != nil || !authoritative {
		return nil, "", err
	}
	snapshot, err := s.transcriptStore.ReadContextInspection(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		return nil, "", err
	}
	if snapshot.Scope.FrameIncarnationID == "" || snapshot.Scope.ConversationID != sessionID {
		return nil, "", nil // Legacy rows cannot manufacture a frame identity.
	}
	return &snapshot, stream.OwnerID, nil
}

func projectContextInspection(authority transcriptstore.ContextInspectionSnapshot, history runnerContextHistory) contextInspection {
	result := contextInspection{Version: 1, Scope: authority.Scope, Calls: []contextInspectionCall{}, Turns: []contextInspectionTurn{},
		Coverage: contextInspectionCoverage{Association: "unavailable", ObservedSampleCount: history.TotalObserved,
			Truncated: authority.Truncated || history.TotalObserved > len(history.Samples)}}
	turns := map[int64]int{}
	inputs := map[int64]transcriptstore.ContextInspectionInput{}
	for _, input := range authority.Inputs {
		turns[input.EventID] = len(result.Turns)
		inputs[input.EventID] = input
		status := "pending"
		if input.InputRevision <= authority.ConsumedInputRevision {
			status = "unknown" // A consumed batch does not prove an individual terminal.
		}
		result.Turns = append(result.Turns, contextInspectionTurn{ID: fmt.Sprintf("%s:input:%d", authority.Scope.StreamUID, input.EventID),
			InputMessageID: input.InputMessageID, InputRevision: input.InputRevision, Status: status, RunnerAttempts: []int64{}, CallIDs: []string{}, Outcomes: []contextInspectionOutcome{}})
	}
	attempts := map[int64]transcriptstore.ContextInspectionAttempt{}
	// Repository attempts are newest first. The latest *durable* state wins;
	// a completed model call never substitutes for a missing runner terminal.
	for _, attempt := range authority.Attempts {
		index, found := turns[attempt.InputEventID]
		if !found {
			continue
		}
		attempts[attempt.Attempt] = attempt
		turn := &result.Turns[index]
		if len(turn.RunnerAttempts) == 0 {
			turn.Status, turn.TerminalEventID, turn.CompletedAt = attempt.Status, attempt.TerminalEventID, attempt.CompletedAt
		}
		turn.RunnerAttempts = append(turn.RunnerAttempts, attempt.Attempt)
	}
	modelCalls, allTyped := 0, true
	for _, sample := range history.Samples {
		if sample.ObservationKind == "context_preparation" {
			continue
		}
		if sample.ObservationKind != "model_call" {
			allTyped = false
			continue // Old observations do not prove a semantic invocation.
		}
		modelCalls++
		association := sample.Association
		if !validContextAssociation(association) {
			result.Coverage.UnassociatedCallCount++
			continue
		}
		if !contextAssociationInScope(association, authority.Scope) || !authority.AncestorBranches[association.Scope.BranchID] {
			continue // Known old incarnation/epoch or another branch's input.
		}
		input, inputFound := inputs[association.InputEventID]
		attempt, attemptFound := attempts[association.RunnerAttempt]
		if !inputFound || !attemptFound || !contextAssociationMatches(association, input, attempt, sample) {
			continue // Never join by timestamp, attempt proximity or message order.
		}
		result.Calls = append(result.Calls, contextInspectionCall{ID: sample.RequestID, InputMessageID: association.InputMessageID,
			InputEventID: association.InputEventID, InputRevision: association.InputRevision, RunnerAttempt: sample.Attempt,
			Model: sample.Model, State: sample.State, ObservedAt: sample.ObservedAt, UsedTokens: sample.UsedTokens,
			LimitTokens: sample.LimitTokens, LimitSource: sample.LimitSource, OutputTokens: sample.OutputTokens, CallUsage: sample.CallUsage})
		turn := &result.Turns[turns[input.EventID]]
		turn.CallIDs = append(turn.CallIDs, sample.RequestID)
		if sample.State == "complete" {
			copy := sample
			turn.Sample = &copy
		}
	}
	result.Coverage.RetainedCallCount = len(result.Calls)
	populateContextInspectionOutcomes(&result, authority, turns, history.Samples)
	if allTyped && len(history.Samples) > 0 && history.TotalObserved == len(history.Samples) {
		result.Coverage.ObservedCallCount = &modelCalls
	}
	if len(result.Calls) > 0 {
		result.Coverage.Association = "partial"
		if result.Coverage.UnassociatedCallCount == 0 && !result.Coverage.Truncated && !authority.OutcomeIncomplete && !contextInspectionHasUnknownTurns(result.Turns) &&
			result.Coverage.ObservedCallCount != nil && len(result.Calls) == *result.Coverage.ObservedCallCount {
			result.Coverage.Association = "complete"
		}
	}
	return result
}

func contextAssociationInScope(value *transcriptstore.ContextCallAssociation, scope transcriptstore.ContextInspectionScope) bool {
	return value.Scope.ConversationID == scope.ConversationID && value.Scope.FrameIncarnationID == scope.FrameIncarnationID &&
		value.Scope.StreamUID == scope.StreamUID && value.Scope.StreamEpoch == scope.StreamEpoch &&
		value.Scope.BranchGeneration <= scope.BranchGeneration && value.Scope.ThroughPublicationSequence <= scope.ThroughPublicationSequence
}

func contextAssociationMatches(value *transcriptstore.ContextCallAssociation, input transcriptstore.ContextInspectionInput, attempt transcriptstore.ContextInspectionAttempt, sample runnerContextUsage) bool {
	return value.InputMessageID == input.InputMessageID && value.InputRevision == input.InputRevision &&
		value.InputPublicationSequence == input.PublicationSequence && value.AdmissionBranchID == input.AdmissionBranchID &&
		value.AdmissionBranchGeneration == input.AdmissionBranchGeneration && value.InputEventID == attempt.InputEventID &&
		attempt.ClaimedRevisions[value.ClaimedInputRevision] && value.RunnerAttempt == int64(sample.Attempt) && sample.SessionID == value.Scope.ConversationID
}
