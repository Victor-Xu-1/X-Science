package memoryextract

import (
	"context"
	"errors"
	"strings"

	"x-science/internal/memoryclassifier"
	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Service) prepareReplacement(ctx context.Context, scope ApplyScope, operation ReplaceOperation, plan *workspace.ExtractionMutationPlan) error {
	memoryID := strings.TrimSpace(operation.ID)
	if memoryID == "" || strings.TrimSpace(operation.Text) == "" {
		return nil
	}
	predecessor, found, err := s.store.GetMemoryOwned(ctx, scope.UserID, memoryID)
	if err != nil {
		return err
	}
	if !found || predecessor.SupersededBy != "" || predecessor.Origin == "user" || predecessor.SubjectFrameID != "" {
		return nil
	}
	inScope, err := s.store.AgentMemoryInScope(ctx, predecessor, scope.UserID, scope.ProjectID, "")
	if err != nil || !inScope {
		return err
	}
	replacement, _, err := workspace.PrepareAgentMemoryBody(operation.Text)
	if err != nil {
		replacementFailure(plan, memoryID, err)
		return nil
	}
	if len(memorytools.MissingDurableLiterals(predecessor.Body, replacement)) > 0 {
		if s.repairer == nil {
			replacementFailure(plan, memoryID, errors.New("replacement drops durable literals and no repairer is configured"))
			return nil
		}
		repaired, repairErr := s.repairer.RepairMemoryReplacement(ctx, predecessor.Body, replacement)
		if repairErr != nil {
			replacementFailure(plan, memoryID, repairErr)
			return nil
		}
		replacement, _, repairErr = workspace.PrepareAgentMemoryBody(repaired)
		if repairErr != nil ||
			len(memorytools.MissingDurableLiterals(predecessor.Body, replacement)) > 0 ||
			len(memorytools.MissingUpdatedDurableLiterals(operation.Text, replacement)) > 0 ||
			len(memorytools.UnexpectedDurableLiterals(replacement, predecessor.Body, operation.Text)) > 0 {
			replacementFailure(plan, memoryID, errors.New("literal repair failed validation"))
			return nil
		}
	}
	if s.classifier == nil {
		replacementFailure(plan, memoryID, memoryclassifier.ErrUnavailable)
		return nil
	}
	classifications, err := s.classifier.ClassifyExtractionWrites(ctx, []string{replacement})
	if errors.Is(err, memoryclassifier.ErrUnavailable) {
		replacementFailure(plan, memoryID, memoryclassifier.ErrUnavailable)
		return nil
	}
	if err != nil {
		return err
	}
	if len(classifications) != 1 {
		return errors.New("memory replacement classifier returned the wrong result count")
	}
	if classifications[0].Flagged {
		plan.Prepared.PIRejected++
		return nil
	}
	plan.Replace = append(plan.Replace, workspace.ExtractionReplacement{
		Expected: predecessor, NewID: s.newID(), Body: replacement, Evidence: strings.TrimSpace(operation.Evidence),
	})
	return nil
}

func replacementFailure(plan *workspace.ExtractionMutationPlan, id string, err error) {
	plan.Prepared.Failures = append(plan.Prepared.Failures, OperationFailure{Kind: "replace", ID: id, Error: err.Error()})
}

func (s *Service) prepareRemoval(ctx context.Context, scope ApplyScope, requestedID string, plan *workspace.ExtractionMutationPlan) error {
	requestedID = strings.TrimSpace(requestedID)
	if requestedID == "" {
		return nil
	}
	active, found, err := s.store.ResolveActiveMemoryOwned(ctx, scope.UserID, requestedID)
	if err != nil {
		return err
	}
	if !found || active.Origin == "user" || active.SubjectFrameID != "" {
		return nil
	}
	inScope, err := s.store.AgentMemoryInScope(ctx, active, scope.UserID, scope.ProjectID, "")
	if err != nil || !inScope {
		return err
	}
	plan.Remove = append(plan.Remove, workspace.ExtractionRemoval{Expected: active})
	return nil
}
