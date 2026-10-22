package memoryextract

import (
	"context"
	"errors"
	"strings"

	"x-science/internal/memoryclassifier"
	"x-science/internal/memorypolicy"
	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"

	"github.com/google/uuid"
)

type ExtractionClassifier interface {
	ClassifyExtractionWrites(context.Context, []string) ([]memorytools.Classification, error)
}

type LiteralRepairer interface {
	RepairMemoryReplacement(context.Context, string, string) (string, error)
}

type ApplyScope struct {
	UserID        string
	ProjectID     string
	SourceFrameID string
}

type OperationFailure = workspace.ExtractionOperationFailure
type ApplyResult = workspace.ExtractionApplyResult

type Service struct {
	store      *workspace.Store
	classifier ExtractionClassifier
	repairer   LiteralRepairer
	newID      func() string
}

func NewService(store *workspace.Store, classifier ExtractionClassifier, repairer LiteralRepairer) *Service {
	return &Service{
		store: store, classifier: classifier, repairer: repairer,
		newID: func() string {
			return "mem_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:memorypolicy.GeneratedIDHexLength]
		},
	}
}

// Prepare performs every model-dependent decision without a write transaction.
// Only CommitMemoryExtraction may persist this plan together with its cursor
// and durable outcome. An error here can never leave early operations applied.
func (s *Service) Prepare(ctx context.Context, scope ApplyScope, operations Operations) (workspace.ExtractionMutationPlan, error) {
	plan := workspace.ExtractionMutationPlan{Prepared: ApplyResult{
		Appended: []string{}, Replaced: []string{}, Removed: []string{}, Failures: []OperationFailure{},
	}}
	if s == nil || s.store == nil {
		return plan, errors.New("memory extraction store is unavailable")
	}
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.ProjectID = strings.TrimSpace(scope.ProjectID)
	scope.SourceFrameID = strings.TrimSpace(scope.SourceFrameID)
	if scope.UserID == "" || scope.SourceFrameID == "" {
		return plan, errors.New("memory extraction user and source frame are required")
	}
	if err := s.prepareAppendBatch(ctx, scope, operations.Append, &plan); err != nil || plan.Prepared.PIUnavailable {
		return plan, err
	}
	for _, operation := range operations.Replace {
		if err := s.prepareReplacement(ctx, scope, operation, &plan); err != nil {
			return plan, err
		}
	}
	for _, requestedID := range operations.Remove {
		if err := s.prepareRemoval(ctx, scope, requestedID, &plan); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func (s *Service) prepareAppendBatch(ctx context.Context, scope ApplyScope, operations []AppendOperation, plan *workspace.ExtractionMutationPlan) error {
	type candidate struct {
		operation AppendOperation
		body      string
	}
	candidates := make([]candidate, 0, len(operations))
	texts := make([]string, 0, len(operations))
	for _, operation := range operations {
		body, _, err := workspace.PrepareAgentMemoryBody(operation.Text)
		if err != nil {
			plan.Prepared.Failures = append(plan.Prepared.Failures, OperationFailure{Kind: "append", Error: err.Error()})
			continue
		}
		if !isFrameEntity(operation.Entity) {
			candidates = append(candidates, candidate{operation: operation, body: body})
			texts = append(texts, body)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if s.classifier == nil {
		plan.Prepared.PIUnavailable = true
		return nil
	}
	classifications, err := s.classifier.ClassifyExtractionWrites(ctx, texts)
	if errors.Is(err, memoryclassifier.ErrUnavailable) {
		plan.Prepared.PIUnavailable = true
		return nil
	}
	if err != nil {
		return err
	}
	if len(classifications) != len(candidates) {
		return errors.New("memory extraction classifier returned the wrong result count")
	}
	for index, item := range candidates {
		if classifications[index].Flagged {
			plan.Prepared.PIRejected++
			continue
		}
		if err := s.prepareAppend(ctx, scope, item.operation, item.body, plan); err != nil {
			return err
		}
	}
	return nil
}
