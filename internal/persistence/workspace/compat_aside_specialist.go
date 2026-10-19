package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

var ErrCompatibilityAsideSpecialistModel = errors.New("aside model exceeds the frozen specialist maximum")

// Nested maps keep the original canonical envelope byte comparison stable after
// its JSON readback. Preserve the integer identities, without a float round-trip.
func compatibilityAsideSpecialistPayload(reference sessionagent.InputReference) map[string]any {
	return map[string]any{
		"scope": map[string]any{
			"frame_id": reference.Scope.FrameID, "frame_incarnation_id": reference.Scope.FrameIncarnationID,
			"root_frame_id": reference.Scope.RootFrameID, "root_incarnation_id": reference.Scope.RootIncarnationID,
		},
		"branch":   map[string]any{"id": reference.Branch.ID, "generation": reference.Branch.Generation},
		"revision": reference.Revision, "snapshot_id": reference.SnapshotID,
	}
}

func normalizeCompatibilityAsideSpecialist(input *CompatibilityAsideInput) error {
	if input.SourceSpecialist == nil {
		return nil
	}
	reference, err := sessionagent.InputReferenceFrom(map[string]any{sessionagent.InputReferenceKey: input.SourceSpecialist})
	if err != nil || input.IntentID == "" || input.SourceBranchID == "" || input.ExpectedGeneration < 1 {
		return sessionagent.ErrInvalid
	}
	input.SourceSpecialist = reference
	return nil
}

// Called only after the original acceptance/cancellation lookup in the same immediate transaction.
func resolveCompatibilityAsideSpecialist(ctx context.Context, tx workspaceTransaction, owner string, input *CompatibilityAsideInput) (*sessionagent.Snapshot, error) {
	if input.SourceSpecialist == nil {
		return nil, nil
	}
	reference := *input.SourceSpecialist
	scope, err := sessionagent.ResolveScope(ctx, tx, owner, input.ParentRootFrameID)
	if err != nil {
		return nil, err
	}
	if scope != reference.Scope {
		return nil, sessionagent.ErrScopeStale
	}
	branch, err := sessionagent.ActiveBranch(ctx, tx, owner, input.ParentRootFrameID)
	if err != nil {
		return nil, err
	}
	if reference.Branch != (sessionagent.Branch{ID: input.SourceBranchID, Generation: input.ExpectedGeneration}) {
		return nil, sessionagent.ErrRevisionConflict
	}
	head, _, _, err := sessionagent.ReadHead(ctx, tx, owner, scope)
	if err != nil {
		return nil, err
	}
	matches, err := sessionagent.ReferenceMatches(ctx, tx, owner, head, reference, branch)
	if err != nil {
		return nil, err
	}
	if !matches {
		return nil, sessionagent.ErrRevisionConflict
	}
	definition, err := sessionagent.ReadSnapshot(ctx, tx, owner, scope, reference.SnapshotID)
	if err != nil {
		return nil, err
	}
	if err := sessionagent.DefinitionAvailable(ctx, tx, owner, definition); err != nil {
		return nil, err
	}
	if input.Model != nil {
		allowed := false
		for _, model := range definition.Settings.AllowedModels {
			allowed = allowed || strings.EqualFold(strings.TrimSpace(*input.Model), strings.TrimSpace(model)) && strings.TrimSpace(model) != ""
		}
		if !allowed {
			return nil, ErrCompatibilityAsideSpecialistModel
		}
	}
	// Model omission means this immutable definition's normal default, not today's
	// parent's mutable model/reviewer override. Other existing session knobs retain their owner.
	input.RuntimeConfig = cloneCompatibilityMap(input.RuntimeConfig)
	if input.RuntimeConfig == nil {
		input.RuntimeConfig = map[string]any{}
	}
	input.RuntimeConfig["model"] = definition.Settings.Model
	delete(input.RuntimeConfig, "reviewer_model")
	return &definition, nil
}

func prepareCompatibilityAsideSpecialist(ctx context.Context, tx *transcriptstore.ImmediateTransaction, owner string, input CompatibilityAsideInput, result *CompatibilityAsideResult, definition *sessionagent.Snapshot) error {
	if definition == nil {
		return nil
	}
	scope, err := sessionagent.ResolveScope(ctx, tx, owner, result.Frame.ID)
	if err != nil {
		return err
	}
	branch, err := sessionagent.ActiveBranch(ctx, tx, owner, result.Frame.ID)
	if err != nil {
		return err
	}
	command := sessionagent.Command{Schema: sessionagent.CommandSchema,
		CommandID: sessionagent.RecordID("aside-transfer", owner, scope.FrameIncarnationID, input.IntentID),
		Scope:     scope, Branch: branch, ExpectedRevision: 0, DesiredAgent: definition.SelectionName}
	prepared, err := sessionagent.Prepare(ctx, tx, owner, command, func(bool) (sessionagent.Preparation, error) {
		return sessionagent.Preparation{Desired: *definition, FallbackUnavailable: true}, nil
	}, result.Frame.CreatedAt)
	if err != nil {
		return err
	}
	child := &sessionagent.InputReference{Scope: scope, Branch: branch, Revision: prepared.Snapshot.Revision, SnapshotID: prepared.Snapshot.Desired.SnapshotID}
	result.Acceptance.SourceSpecialist = input.SourceSpecialist
	result.Acceptance.SpecialistBinding = child
	result.InputData[sessionagent.InputReferenceKey] = child
	return nil
}

// Update the existing envelope and metadata, never another receipt or transaction.
func writeCompatibilityAsideSpecialistProof(ctx context.Context, tx workspaceTransaction, owner string, input CompatibilityAsideInput, result CompatibilityAsideResult) error {
	if input.SourceSpecialist == nil {
		return nil
	}
	raw, err := marshalCompatibilityQueueEnvelope(compatibilityAsideIntentPayload(input, owner), map[string]any{
		"frame_id": result.Frame.ID, "request": input.Request, "aside_acceptance": result.Acceptance,
	}, "")
	if err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE queued_user_messages SET payload=? WHERE intent_id=? AND frame_id=? AND state='drained'`, raw, input.IntentID, result.Frame.ID)
	if err != nil {
		return err
	}
	if count, err := updated.RowsAffected(); err != nil || count != 1 {
		return ErrCompatibilityAsideAcceptanceUnavailable
	}
	data, err := json.Marshal(result.InputData)
	if err != nil {
		return err
	}
	updated, err = tx.ExecContext(ctx, `UPDATE frame_runtime_metadata SET input_data=? WHERE frame_id=?`, string(data), result.Frame.ID)
	if err != nil {
		return err
	}
	if count, err := updated.RowsAffected(); err != nil || count != 1 {
		return ErrCompatibilityAsideAcceptanceUnavailable
	}
	return nil
}

func validateCompatibilityAsideSourceProof(payload map[string]any, proof *sessionagent.InputReference, parent, incarnation string) error {
	value, hasSource := payload["source_specialist"]
	if !hasSource {
		if proof != nil {
			return ErrCompatibilityAsideAcceptanceUnavailable
		}
		return nil
	}
	decoded, err := sessionagent.InputReferenceFrom(map[string]any{sessionagent.InputReferenceKey: value})
	if err != nil || proof == nil || *proof != *decoded || proof.Scope.FrameID != parent || proof.Scope.RootFrameID != parent ||
		proof.Scope.FrameIncarnationID != incarnation || proof.Scope.RootIncarnationID != incarnation {
		return ErrCompatibilityAsideAcceptanceUnavailable
	}
	return nil
}

func validateCompatibilityAsideSpecialistEnvelope(payload map[string]any, accepted CompatibilityAsideAcceptance) error {
	if err := validateCompatibilityAsideSourceProof(payload, accepted.SourceSpecialist, accepted.ParentRootFrameID, accepted.ParentIncarnationID); err != nil {
		return err
	}
	if accepted.SourceSpecialist == nil {
		if accepted.SpecialistBinding != nil {
			return ErrCompatibilityAsideAcceptanceUnavailable
		}
		return nil
	}
	child := accepted.SpecialistBinding
	if child == nil || !sessionagent.ValidScope(child.Scope) || child.Scope.FrameID != accepted.FrameID || child.Scope.RootFrameID != accepted.FrameID || child.Scope.RootIncarnationID != child.Scope.FrameIncarnationID ||
		child.SnapshotID != accepted.SourceSpecialist.SnapshotID || child.Revision != 1 || child.Branch.ID == "" || child.Branch.Generation < 1 ||
		accepted.SourceSpecialist.Branch != (sessionagent.Branch{ID: accepted.SourceBranchID, Generation: accepted.SourceGeneration}) {
		return ErrCompatibilityAsideAcceptanceUnavailable
	}
	return nil
}

func validateCompatibilityAsideSpecialistProof(ctx context.Context, tx workspaceTransaction, owner string, frame Frame, payload map[string]any, accepted CompatibilityAsideAcceptance) error {
	if err := validateCompatibilityAsideSpecialistEnvelope(payload, accepted); err != nil || accepted.SpecialistBinding == nil {
		return err
	}
	child := accepted.SpecialistBinding
	if child.Scope.FrameIncarnationID != frame.IncarnationID {
		return ErrCompatibilityAsideAcceptanceUnavailable
	}
	binding, err := sessionagent.ReadInput(ctx, tx, owner, child.Scope, accepted.InputMessageID)
	if err != nil {
		return err
	}
	if binding == nil || binding.Branch != child.Branch || binding.Selection.SnapshotID != child.SnapshotID || binding.Selection.Revision != child.Revision {
		return ErrCompatibilityAsideAcceptanceUnavailable
	}
	_, err = sessionagent.ReadSnapshot(ctx, tx, owner, child.Scope, child.SnapshotID)
	return err
}
