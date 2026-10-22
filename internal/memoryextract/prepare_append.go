package memoryextract

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Service) prepareAppend(ctx context.Context, scope ApplyScope, operation AppendOperation, body string, plan *workspace.ExtractionMutationPlan) error {
	projectID, artifactID, ok, err := s.resolveAppendEntity(ctx, scope, operation.Entity)
	if err != nil || !ok {
		return err
	}
	categoryID := ""
	if categoryName := strings.TrimSpace(operation.Category); categoryName != "" {
		categoryID, err = s.store.MemoryCategoryIDByName(ctx, scope.UserID, categoryName)
		if errors.Is(err, sql.ErrNoRows) {
			categoryID, err = "", nil
		}
		if err != nil {
			return err
		}
	}
	plan.Append = append(plan.Append, workspace.AgentMemoryAppendMutation{Input: workspace.CreateMemoryInput{
		ID: s.newID(), UserID: scope.UserID, Body: body,
		Origin: "extractor", Evidence: normalizedEvidence(operation.Evidence), SubjectProjectID: projectID,
		SubjectArtifactID: artifactID, SourceFrameID: scope.SourceFrameID, CategoryID: categoryID,
	}})
	return nil
}

func (s *Service) resolveAppendEntity(ctx context.Context, scope ApplyScope, raw string) (string, string, bool, error) {
	if isFrameEntity(raw) {
		return "", "", false, nil
	}
	entity := extractorAppendEntity(raw, scope.ProjectID)
	if entity == "profile" && scope.ProjectID != "" {
		entity = "project:" + scope.ProjectID
	}
	resolved, err := s.store.ResolveMemoryEntityScope(ctx, scope.UserID, entity)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if scope.ProjectID != "" && resolved.OwningProjectID != "" && resolved.OwningProjectID != scope.ProjectID {
		return "", "", false, nil
	}
	return resolved.SubjectProjectID, resolved.SubjectArtifactID, true, nil
}

func extractorAppendEntity(raw, currentProjectID string) string {
	fallback := "profile"
	if currentProjectID != "" {
		fallback = "project:" + currentProjectID
	}
	entity := strings.TrimSpace(raw)
	switch {
	case entity == "", entity == "project":
		return fallback
	case entity == "profile":
		return entity
	case validExtractorEntityID(entity, "project:"), validExtractorEntityID(entity, "artifact:"):
		return entity
	default:
		return fallback
	}
}

func validExtractorEntityID(entity, prefix string) bool {
	if !strings.HasPrefix(entity, prefix) {
		return false
	}
	id := strings.TrimPrefix(entity, prefix)
	return id != "" && strings.IndexFunc(id, unicode.IsSpace) == -1
}

func isFrameEntity(value string) bool {
	value = strings.TrimSpace(value)
	return value == "frame" || strings.HasPrefix(value, "frame:")
}
