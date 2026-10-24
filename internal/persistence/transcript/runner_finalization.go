package transcript

import (
	"context"
	"encoding/json"
	"strings"
)

// RunnerFinalizationInput names the exact existing canonical operations. No
// candidate journal, replacement claim or cross-token execution is introduced.
type RunnerFinalizationInput struct {
	Assistant *AppendEventInput
	Finish    FinishRunnerInput
}

type RunnerFinalizationArtifactBinding struct {
	SourceEventID, BoundEventID int64
	Ordinal                     int
	ArtifactID, VersionID       string
	Relation                    ArtifactRelation
}

type RunnerFinalizationReceipt struct {
	Stream              Stream
	BranchID            string
	BranchGeneration    int64
	Assistant           Event
	AssistantReferences []ArtifactReference
	Terminal            Event
	TerminalReferences  []ArtifactReference
	Finish              FinishReceipt
	ArtifactBindings    []RunnerFinalizationArtifactBinding
}

type RunnerFinalizationOutcome struct {
	Transaction       ImmediateTransactionOutcome
	ReadbackConfirmed bool
}

func (outcome RunnerFinalizationOutcome) KnownCommit() bool {
	return outcome.Transaction.Committed || outcome.ReadbackConfirmed
}

// RunnerFinalizationAssistantPayloadError marks only a deterministic failure of
// the original payload validator, before this finalization can attempt COMMIT.
// Claim/branch/owner and SQL errors never acquire this classification.
type RunnerFinalizationAssistantPayloadError struct{ cause error }

func (err *RunnerFinalizationAssistantPayloadError) Error() string {
	if err.cause == nil {
		return "invalid final assistant payload"
	}
	return "invalid final assistant payload: " + err.cause.Error()
}
func (err *RunnerFinalizationAssistantPayloadError) Unwrap() error { return err.cause }

func ValidateRunnerFinalizationInput(input RunnerFinalizationInput) error {
	if err := validateClaimInput(input.Finish.Claim); err != nil {
		return err
	}
	if strings.TrimSpace(input.Finish.ClientMessageID) == "" || !terminalStatus(input.Finish.Status) {
		return ErrEventConflict
	}
	if err := validatePayload(EventSourcePayload, input.Finish.PayloadJSON, nil); err != nil {
		return err
	}
	if input.Assistant != nil {
		if !sameFinalizationClaim(input.Assistant.Claim, input.Finish.Claim) || input.Assistant.Type != "assistant_message" ||
			input.Assistant.ClientMessageID == input.Finish.ClientMessageID {
			return ErrEventConflict
		}
		if err := validatePayload(input.Assistant.Source, input.Assistant.PayloadJSON, input.Assistant.FrameEventID); err != nil {
			return &RunnerFinalizationAssistantPayloadError{cause: err}
		}
		if err := validateRunnerEventInput(*input.Assistant); err != nil {
			return err
		}
	}
	return nil
}

// FinalizeRunner composes the original typed assistant/reference and terminal
// operations on this already-held connection. Repository wrappers are never
// invoked inside the transaction, and old partial pairs are not silently filled.
func (tx *ImmediateTransaction) FinalizeRunner(ctx context.Context, input RunnerFinalizationInput) (RunnerFinalizationReceipt, bool, error) {
	if err := ValidateRunnerFinalizationInput(input); err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	receipt, found, err := tx.InspectRunnerFinalization(ctx, input, nil)
	if err != nil || found {
		return receipt, false, err
	}
	if cancelled, found, err := tx.inspectCancelledRunnerFinalization(ctx, input); err != nil || found {
		return cancelled, false, err
	}
	if input.Assistant != nil {
		if _, _, err := tx.AppendFinalAssistant(ctx, *input.Assistant); err != nil {
			return RunnerFinalizationReceipt{}, false, err
		}
	}
	_, _, created, err := tx.FinishRunner(ctx, input.Finish)
	if err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	receipt, found, err = tx.InspectRunnerFinalization(ctx, input, nil)
	if err == nil && !found {
		err = ErrEventConflict
	}
	return receipt, created, err
}

// AppendFinalAssistant is also usable by fixtures/nonterminal publishers; the
// canonical finalizer composes this same core with FinishRunner before COMMIT.
func (tx *ImmediateTransaction) AppendFinalAssistant(ctx context.Context, assistant AppendEventInput) (Event, []ArtifactReference, error) {
	if tx == nil || tx.repository == nil || tx.conn == nil {
		return Event{}, nil, ErrSchemaUnavailable
	}
	if assistant.Type != "assistant_message" || assistant.Source != EventSourcePayload {
		return Event{}, nil, ErrEventConflict
	}
	snapshot, err := tx.CurrentArtifactCommitSnapshot(ctx, assistant.Claim.StreamUID, assistant.Claim.OwnerID, assistant.Claim.Attempt)
	if err != nil {
		return Event{}, nil, err
	}
	references, err := FinalAssistantArtifactReferences(snapshot, assistant.Claim.Attempt, assistant.PayloadJSON)
	if err != nil {
		return Event{}, nil, err
	}
	if len(references) > 0 {
		_, _, _, err = tx.AppendAssistantEventWithArtifacts(ctx, AppendAssistantEventWithArtifactsInput{
			Claim: assistant.Claim, ClientMessageID: assistant.ClientMessageID, Source: assistant.Source, PayloadJSON: assistant.PayloadJSON,
			FrameEventID: assistant.FrameEventID, Destinations: assistant.Destinations, References: references,
		})
		if err != nil {
			return Event{}, nil, err
		}
	}
	event, refs, _, err := tx.AppendAssistantEventWithCommittedArtifacts(ctx, assistant)
	return event, refs, err
}

// FinalizeRunner keeps standalone Transcript consumers on the same atomic core;
// Workspace adds only its original Memory and Aside effects before the commit.
func (r *Repository) FinalizeRunner(ctx context.Context, input RunnerFinalizationInput) (RunnerFinalizationReceipt, RunnerFinalizationOutcome, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		return RunnerFinalizationReceipt{}, RunnerFinalizationOutcome{}, ErrEventConflict
	}
	var receipt RunnerFinalizationReceipt
	transaction, err := r.RunImmediateWithOutcome(ctx, func(tx *ImmediateTransaction) error {
		var err error
		receipt, _, err = tx.FinalizeRunner(ctx, input)
		return err
	})
	outcome := RunnerFinalizationOutcome{Transaction: transaction}
	if err != nil && transaction.CommitAttempted {
		confirmed, found, readErr := r.ReadRunnerFinalization(ctx, input, &receipt, nil)
		if readErr == nil && found {
			outcome.ReadbackConfirmed = true
			return confirmed, outcome, nil
		}
	}
	return receipt, outcome, err
}

// This is the previous final-answer selection, now executed inside the atomic
// connection: current heads plus explicitly cited prior immutable versions.
func FinalAssistantArtifactReferences(snapshot ArtifactCommitSnapshot, attempt int64, payloadJSON []byte) ([]ArtifactReferenceInput, error) {
	if attempt <= 0 || snapshot.ThroughAttempt != attempt {
		return nil, ErrEventConflict
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, err
	}
	references := make([]ArtifactReferenceInput, 0, len(snapshot.References))
	seen := map[ArtifactReferenceInput]struct{}{}
	for _, reference := range snapshot.References {
		if reference.Availability != ArtifactAvailable || reference.RunnerAttempt > snapshot.ThroughAttempt {
			continue
		}
		prior := reference.RunnerAttempt != snapshot.ThroughAttempt
		if prior && !strings.Contains(payload.Text, "{{artifact:"+reference.VersionID+"}}") {
			continue
		}
		relation := reference.Relation
		if prior {
			relation = ArtifactRelationCited
		}
		input := ArtifactReferenceInput{ArtifactID: reference.ArtifactID, VersionID: reference.VersionID, Relation: relation}
		if _, duplicate := seen[input]; !duplicate {
			seen[input] = struct{}{}
			references = append(references, input)
		}
	}
	return references, nil
}
