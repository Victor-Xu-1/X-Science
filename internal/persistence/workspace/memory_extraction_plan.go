package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"x-science/internal/memorypolicy"
)

func normalizeExtractionPlan(source CompletedExtractionSource, plan ExtractionMutationPlan) (ExtractionMutationPlan, error) {
	if len(plan.Append) > memorypolicy.OperationsPerKindMax || len(plan.Replace) > memorypolicy.OperationsPerKindMax || len(plan.Remove) > memorypolicy.OperationsPerKindMax {
		return ExtractionMutationPlan{}, errors.New("memory extraction operation limit exceeded")
	}
	if plan.Prepared.PIUnavailable || len(plan.Prepared.Appended)+len(plan.Prepared.Replaced)+len(plan.Prepared.Removed) != 0 || plan.Prepared.PIRejected < 0 {
		return ExtractionMutationPlan{}, errors.New("prepared memory extraction cannot claim persisted outcomes")
	}
	plan.Append = append([]AgentMemoryAppendMutation(nil), plan.Append...)
	plan.Replace = append([]ExtractionReplacement(nil), plan.Replace...)
	plan.Remove = append([]ExtractionRemoval(nil), plan.Remove...)
	plan.Prepared = cloneExtractionApplyResult(plan.Prepared)
	ids := make(map[string]bool)
	for index := range plan.Append {
		input := plan.Append[index].Input
		input.UserID, input.Origin, input.SourceFrameID = source.OwnerUserID, "extractor", source.FrameID
		if input.SubjectFrameID != "" {
			return ExtractionMutationPlan{}, ErrAgentMemoryMutationForbidden
		}
		var err error
		input, err = normalizeMemoryInput(input)
		if err != nil {
			return ExtractionMutationPlan{}, err
		}
		if ids[input.ID] {
			return ExtractionMutationPlan{}, errors.New("duplicate memory extraction successor id")
		}
		ids[input.ID] = true
		plan.Append[index].Input = input
	}
	for index := range plan.Replace {
		replacement := plan.Replace[index]
		replacement.Expected = extractionMemoryExpectation(replacement.Expected)
		if err := validateExtractionExpectation(source, replacement.Expected); err != nil {
			return ExtractionMutationPlan{}, err
		}
		candidate, err := normalizeMemoryInput(CreateMemoryInput{
			ID: replacement.NewID, UserID: source.OwnerUserID, Body: replacement.Body,
			Origin: replacement.Expected.Origin, Evidence: replacement.Evidence,
		})
		if err != nil {
			return ExtractionMutationPlan{}, err
		}
		if ids[candidate.ID] || candidate.ID == replacement.Expected.ID {
			return ExtractionMutationPlan{}, errors.New("duplicate memory extraction successor id")
		}
		ids[candidate.ID] = true
		replacement.NewID, replacement.Body = candidate.ID, candidate.Body
		if strings.TrimSpace(replacement.Evidence) != "" {
			replacement.Evidence = candidate.Evidence
		}
		plan.Replace[index] = replacement
	}
	for index := range plan.Remove {
		plan.Remove[index].Expected = extractionMemoryExpectation(plan.Remove[index].Expected)
		if err := validateExtractionExpectation(source, plan.Remove[index].Expected); err != nil {
			return ExtractionMutationPlan{}, err
		}
	}
	return plan, nil
}

func validateExtractionExpectation(source CompletedExtractionSource, expected Memory) error {
	if expected.ID == "" || expected.UserID != source.OwnerUserID || !validAgentMutableMemoryOrigin(expected.Origin) ||
		expected.SubjectFrameID != "" || expected.SupersededBy != "" {
		return ErrAgentMemoryMutationForbidden
	}
	return nil
}

// Only compare the same authoritative snapshot fields used by replacement CAS;
// recall scores, display names and access timestamps are not mutation authority.
func extractionMemoryExpectation(memory Memory) Memory {
	return Memory{
		ID: memory.ID, UserID: memory.UserID, Body: memory.Body, Evidence: memory.Evidence,
		Origin: memory.Origin, SupersededBy: memory.SupersededBy, SubjectProjectID: memory.SubjectProjectID,
		SubjectArtifactID: memory.SubjectArtifactID, SubjectVersionID: memory.SubjectVersionID,
		SubjectFrameID: memory.SubjectFrameID, CategoryID: memory.CategoryID,
	}
}

func cloneExtractionApplyResult(result ExtractionApplyResult) ExtractionApplyResult {
	result.Appended = append([]string(nil), result.Appended...)
	result.Replaced = append([]string(nil), result.Replaced...)
	result.Removed = append([]string(nil), result.Removed...)
	result.Failures = append([]ExtractionOperationFailure(nil), result.Failures...)
	return result
}

func extractionPlanDigest(input ExtractionCommitInput) (string, error) {
	encoded, err := json.Marshal(struct {
		Plan                     ExtractionMutationPlan
		CursorBefore, CursorNext int
		AdvanceCursor, Overshoot bool
		DeltaCount               int
		Gate                     string
		ModeDecision             *MemoryExtractionModeDecision `json:",omitempty"`
	}{input.Plan, input.CursorBefore, input.CursorNext, input.AdvanceCursor, input.Overshoot, input.DeltaCount, input.Gate, input.ModeDecision})
	if err != nil {
		return "", err
	}
	return extractionDigest(encoded), nil
}

func (s *Store) applyExtractionPlanTx(ctx context.Context, tx memoryTransaction, source CompletedExtractionSource, plan ExtractionMutationPlan) (ExtractionApplyResult, error) {
	result := cloneExtractionApplyResult(plan.Prepared)
	for _, mutation := range plan.Append {
		input := mutation.Input
		// A category can be deleted while classification is running. Preserve the
		// prior unfiled fallback, now within the same atomic transaction.
		if input.CategoryID != "" {
			var owner string
			err := tx.QueryRowContext(ctx, `SELECT user_id FROM memory_categories WHERE id=?`, input.CategoryID).Scan(&owner)
			if errors.Is(err, sql.ErrNoRows) {
				input.CategoryID = ""
			} else if err != nil {
				return ExtractionApplyResult{}, fmt.Errorf("read extractor category: %w", err)
			} else if owner != source.OwnerUserID {
				return ExtractionApplyResult{}, ErrAgentMemoryMutationForbidden
			}
		}
		project, err := validateMemoryScopeTx(ctx, tx, input, source.OwnerUserID)
		if err != nil {
			return ExtractionApplyResult{}, err
		}
		if project != "" && project != source.ProjectID {
			return ExtractionApplyResult{}, ErrAgentMemoryMutationForbidden
		}
		memory, err := s.createMemoryTx(ctx, tx, input, source.OwnerUserID)
		if err != nil {
			return ExtractionApplyResult{}, err
		}
		result.Appended = append(result.Appended, memory.ID)
	}
	for _, replacement := range plan.Replace {
		_, err := s.createAndSupersedeExtractorMemoryTx(ctx, tx, replacement.Expected, source.OwnerUserID,
			source.ProjectID, source.FrameID, replacement.NewID, replacement.Body, replacement.Evidence)
		if errors.Is(err, ErrAgentMemoryChanged) {
			result.Failures = append(result.Failures, ExtractionOperationFailure{Kind: "replace", ID: replacement.Expected.ID, Error: err.Error()})
			continue
		}
		if err != nil {
			return ExtractionApplyResult{}, err
		}
		result.Replaced = append(result.Replaced, replacement.Expected.ID)
	}
	for _, removal := range plan.Remove {
		expected := removal.Expected
		current, err := scanMemory(tx.QueryRowContext(ctx, memorySelect+` WHERE m.id=? AND m.user_id=?`, expected.ID, source.OwnerUserID))
		if errors.Is(err, sql.ErrNoRows) || err == nil && (current.SupersededBy != "" || !sameExtractorMemorySnapshot(current, expected)) {
			result.Failures = append(result.Failures, ExtractionOperationFailure{Kind: "remove", ID: expected.ID, Error: ErrAgentMemoryChanged.Error()})
			continue
		}
		if err != nil {
			return ExtractionApplyResult{}, fmt.Errorf("read extractor removal: %w", err)
		}
		if err := requireAgentMemoryScopeTx(ctx, tx, current, source.OwnerUserID, source.ProjectID, ""); err != nil {
			return ExtractionApplyResult{}, err
		}
		if err := validateAgentMemoryRemovalChainTx(ctx, tx, current.ID, source.OwnerUserID, source.ProjectID, ""); err != nil {
			return ExtractionApplyResult{}, err
		}
		applied, err := s.applyAgentMemoryMutationBatchTx(ctx, tx, AgentMemoryMutationBatch{
			OwnerUserID: source.OwnerUserID, ProjectID: source.ProjectID, FrameID: source.FrameID,
			Remove: []AgentMemoryRemoveMutation{{ActiveID: current.ID, ExpectedOrigin: current.Origin}},
		})
		if err != nil {
			return ExtractionApplyResult{}, err
		}
		result.Removed = append(result.Removed, applied.Removed...)
	}
	return result, nil
}
