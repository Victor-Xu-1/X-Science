package server

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	sessionstore "x-science/internal/persistence/sessions"
)

// Ephemeral projection of the canonical delivery, not a new artifact authority.
// Only selected manifests can add published reference context to this scope.
type structureSceneDeliveryScope struct {
	artifacts          []sessionReviewerArtifactEvidence
	producedStructures map[string]struct{}
}

func (s *Server) sessionRunnerStructureSceneScope(session sessionstore.Session, rootFrameID string, delivery []sessionReviewerArtifactEvidence) (*structureSceneDeliveryScope, error) {
	scope := &structureSceneDeliveryScope{
		artifacts:          append([]sessionReviewerArtifactEvidence(nil), delivery...),
		producedStructures: make(map[string]struct{}),
	}
	selected := make(map[string]struct{}, len(delivery))
	references := make(map[string]struct{})
	hasManifest := false
	for _, artifact := range delivery {
		selected[artifact.VersionID] = struct{}{}
		if structureArtifactName(artifact.Name) {
			scope.producedStructures[artifact.VersionID] = struct{}{}
		}
		if !strings.EqualFold(filepath.Ext(artifact.Name), ".json") {
			continue
		}
		content, err := s.readStructureSceneArtifact(artifact.VersionID)
		if err != nil {
			continue // The existing manifest validator reports unreadable comparisons.
		}
		var envelope struct {
			Schema string `json:"schema"`
		}
		if json.Unmarshal(content, &envelope) != nil || envelope.Schema != structureSceneManifestSchema {
			continue
		}
		hasManifest = true
		var manifest structureSceneManifest
		if json.Unmarshal(content, &manifest) != nil {
			continue // A typed malformed manifest remains an advisory, never a pass.
		}
		for _, reference := range append([]structureSceneArtifactRef{manifest.MotherStructure}, manifest.DerivedStructures...) {
			if versionID := strings.TrimSpace(reference.VersionID); versionID != "" {
				references[versionID] = struct{}{}
			}
		}
		if versionID := strings.TrimSpace(manifest.PreviewImage.VersionID); versionID != "" {
			references[versionID] = struct{}{}
		}
	}
	if len(scope.producedStructures) < 2 && !hasManifest {
		return nil, nil
	}
	if len(references) == 0 {
		return scope, nil
	}
	// Reuse the original same-root/project, current-version inventory only as
	// lookup context. Its other structures and manifests do not enter delivery.
	inventory, err := s.sessionReviewerWorkspaceEvidence(session, rootFrameID)
	if err != nil {
		return nil, err
	}
	for _, artifact := range inventory.Artifacts {
		if _, needed := references[artifact.VersionID]; !needed {
			continue
		}
		if _, produced := selected[artifact.VersionID]; produced {
			continue
		}
		// References supply structures or image bytes, never another manifest
		// that could replace the currently selected comparison contract.
		if !structureArtifactName(artifact.Name) && !visualArtifactName(artifact.Name) {
			continue
		}
		retention, found, err := s.workspaceStore.ArtifactRetentionMode(artifact.ArtifactID)
		if err != nil {
			return nil, fmt.Errorf("read structure scene retention for %s: %w", artifact.Name, err)
		}
		if !found || retention != "snapshot" {
			continue
		}
		intermediate, _, err := s.workspaceStore.ArtifactVersionIntermediate(artifact.VersionID)
		if err != nil {
			return nil, fmt.Errorf("read structure scene version state for %s: %w", artifact.Name, err)
		}
		if !intermediate {
			scope.artifacts = append(scope.artifacts, artifact)
		}
	}
	return scope, nil
}

func structureSceneDeliveryCoverage(manifest structureSceneManifest, produced map[string]struct{}) []string {
	covered := map[string]struct{}{strings.TrimSpace(manifest.MotherStructure.VersionID): {}}
	for _, reference := range manifest.DerivedStructures {
		covered[strings.TrimSpace(reference.VersionID)] = struct{}{}
	}
	var failures []string
	for versionID := range produced {
		if _, found := covered[versionID]; !found {
			failures = append(failures, "scene omits current produced structure version "+versionID)
		}
	}
	return uniqueSortedStrings(failures)
}
