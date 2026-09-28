package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	sessionstore "synon-go/internal/persistence/sessions"
	transcriptstore "synon-go/internal/persistence/transcript"
)

const sessionRunnerVisualArtifactValidationReasonCode = "visual_artifact_validation_required"

type sessionRunnerVisualArtifactValidationRequired struct {
	Artifacts              []string
	Targets                []transcriptstore.RunnerVisualConditionArtifact
	StructureSceneFailures []string
}

func (err *sessionRunnerVisualArtifactValidationRequired) Error() string {
	if err == nil {
		return "generated visual artifacts require immutable visual validation"
	}
	// Keep repair causes separate from affected names: a large delivery must
	// not consume the display budget before the model sees what to fix.
	failures := summarizeVisualValidationItems(err.StructureSceneFailures)
	names := summarizeVisualValidationItems(err.Artifacts)
	if failures == "" && names == "" {
		return "generated visual artifacts require immutable visual validation"
	}
	if failures != "" {
		detail := "multi-structure delivery does not satisfy the immutable structure-scene contract: " + failures
		if names != "" {
			detail += ". Affected artifacts: " + names
		}
		return detail
	}
	return "generated visual artifacts or structure scenes are not bound to the required immutable visual evidence: " + names
}

func summarizeVisualValidationItems(items []string) string {
	items = uniqueSortedStrings(items)
	if len(items) > 8 {
		items = append(items[:8], fmt.Sprintf("and %d more", len(items)-8))
	}
	return strings.Join(items, ", ")
}

func (err *sessionRunnerVisualArtifactValidationRequired) runnerCorrection() transcriptstore.RunnerInterruptionCause {
	var artifacts []string
	if err != nil {
		artifacts = append([]string(nil), err.Artifacts...)
		artifacts = append(artifacts, err.StructureSceneFailures...)
	}
	artifacts = uniqueSortedStrings(artifacts)
	visual := transcriptstore.RunnerVisualCondition{UnboundNames: artifacts}
	if err != nil && len(err.Targets) > 0 {
		visual.Artifacts = append([]transcriptstore.RunnerVisualConditionArtifact(nil), err.Targets...)
		visual.UnboundNames = append([]string(nil), err.StructureSceneFailures...)
	}
	detail := err.Error() + ". For multiple structures, save a synon.structure-scene.v1 manifest using mother_structure and derived_structures entries with name/version_id, layers with version_id/role/representation/visible, and preview_image with name/version_id/sha256. Use the native Mol* preview and the cheminfo-render contract; do not generate a standalone HTML viewer or substitute a 2D grid for a 3D scene. Preserve coordinates and exact immutable hashes; repair the reported scene or manifest fields."
	return newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode,
		detail,
		transcriptstore.RunnerCorrectionCondition{Visual: &visual},
	)
}

// verifySessionRunnerVisualArtifactEvidence is the deterministic image gate for
// the user-controlled completion-verification pipeline. Optional independent
// review may add broader judgment, but verifier_mode=off must not start or keep
// a review workflow alive after the main task has completed.
func (s *Server) verifySessionRunnerVisualArtifactEvidence(session sessionstore.Session, candidates []transcriptstore.ArtifactReferenceInput) error {
	// Not every runner entry point is backed by a workspace frame. IM,
	// delegation, and direct TaskRun sessions can legitimately complete without
	// one, and therefore cannot have workspace artifact versions to validate.
	// Byte integrity is checked here. Independent interpretation belongs to the
	// existing reviewer, not a retired tool that the main model cannot invoke.
	if s == nil || s.workspaceStore == nil {
		return nil
	}
	frame, found, err := s.workspaceStore.GetFrame(session.ID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	rootFrameID := strings.TrimSpace(frame.RootFrameID)
	if rootFrameID == "" {
		rootFrameID = strings.TrimSpace(frame.ID)
	}
	workspaceEvidence, err := s.sessionReviewerWorkspaceEvidence(session, rootFrameID)
	if err != nil {
		return err
	}
	if err := s.verifyVisualArtifactEvidence(workspaceEvidence.Artifacts); err != nil {
		return err
	}
	return s.verifyStructureSceneEvidence(workspaceEvidence.Artifacts, candidates)
}

// Validate actual immutable bytes without treating metadata as semantic review.
func (s *Server) verifyVisualArtifactEvidence(artifacts []sessionReviewerArtifactEvidence) error {
	for _, artifact := range artifacts {
		if !visualArtifactName(artifact.Name) {
			continue
		}
		if s == nil || s.workspaceStore == nil {
			return errors.New("visual artifact store is unavailable")
		}
		_, version, reader, found, err := s.workspaceStore.OpenArtifactVersionContent(artifact.VersionID)
		if err != nil {
			return err
		}
		if !found || reader == nil {
			return fmt.Errorf("visual artifact %s is unavailable", artifact.Name)
		}
		hash := sha256.New()
		header := make([]byte, 512)
		n, readErr := io.ReadFull(reader, header)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			reader.Close()
			return readErr
		}
		_, _ = hash.Write(header[:n])
		_, copyErr := io.Copy(hash, reader)
		closeErr := reader.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		if !agentWorkspaceImageMIME(http.DetectContentType(header[:n])) ||
			!strings.EqualFold(digest, artifact.ContentSHA256) ||
			!strings.EqualFold(digest, version.ContentSHA256) {
			return &sessionRunnerVisualArtifactValidationRequired{
				Artifacts: []string{artifact.Name},
				Targets:   []transcriptstore.RunnerVisualConditionArtifact{{Name: artifact.Name, VersionID: artifact.VersionID, SHA256: artifact.ContentSHA256, Failure: "invalid_image_integrity"}},
			}
		}
	}
	return nil
}

func visualArtifactName(name string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}
