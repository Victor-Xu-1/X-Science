package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

const sessionRunnerVisualArtifactValidationReasonCode = "visual_artifact_validation_required"

type sessionRunnerVisualArtifactValidationRequired struct {
	Artifacts []string
	Targets   []transcriptstore.RunnerVisualConditionArtifact
}

func (err *sessionRunnerVisualArtifactValidationRequired) Error() string {
	if err == nil {
		return "generated visual artifacts require immutable visual validation"
	}
	names := summarizeVisualValidationItems(err.Artifacts)
	if names == "" {
		return "generated visual artifacts require immutable visual validation"
	}
	return "generated image artifacts failed immutable byte integrity validation: " + names
}

func summarizeVisualValidationItems(items []string) string {
	items = uniqueSortedStrings(items)
	if len(items) > 8 {
		items = append(items[:8], fmt.Sprintf("and %d more", len(items)-8))
	}
	return strings.Join(items, ", ")
}

func (err *sessionRunnerVisualArtifactValidationRequired) runnerCorrection() transcriptstore.RunnerInterruptionCause {
	// New hard failures only describe actual image targets. The historical
	// scene-only UnboundNames shape is read for compatibility, never emitted.
	visual := transcriptstore.RunnerVisualCondition{}
	if err != nil {
		visual.Artifacts = append([]transcriptstore.RunnerVisualConditionArtifact(nil), err.Targets...)
	}
	detail := err.Error() + ". Repair the reported image bytes or immutable hash binding. Preserve completed work and do not claim that invalid image content has been verified."
	return newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode,
		detail,
		transcriptstore.RunnerCorrectionCondition{Visual: &visual},
	)
}

// verifySessionRunnerVisualArtifactEvidence is the deterministic image gate for
// the user-controlled completion-verification pipeline. Optional independent
// review may add broader judgment, but verifier_mode=off must not start or keep
// a review workflow alive after the main task has completed.
func (s *Server) verifySessionRunnerVisualArtifactEvidence(session sessionstore.Session, candidates []transcriptstore.ArtifactReferenceInput) (*sessionRunnerCompletionAdvisory, error) {
	// Not every runner entry point is backed by a workspace frame. IM,
	// delegation, and direct TaskRun sessions can legitimately complete without
	// one, and therefore cannot have workspace artifact versions to validate.
	// Byte integrity is checked here. Independent interpretation belongs to the
	// existing reviewer, not a retired tool that the main model cannot invoke.
	if s == nil || s.workspaceStore == nil {
		return nil, nil
	}
	frame, found, err := s.workspaceStore.GetFrame(session.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	rootFrameID := strings.TrimSpace(frame.RootFrameID)
	if rootFrameID == "" {
		rootFrameID = strings.TrimSpace(frame.ID)
	}
	workspaceEvidence, err := s.sessionReviewerWorkspaceEvidence(session, rootFrameID)
	if err != nil {
		return nil, err
	}
	deliveryEvidence, err := s.sessionRunnerVisualDeliveryEvidence(frame.ProjectID, candidates)
	if err != nil {
		return nil, err
	}
	if err := s.verifyVisualArtifactEvidence(deliveryEvidence); err != nil {
		return nil, err
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
		content, readErr := io.ReadAll(io.LimitReader(reader, agentWorkspaceVisualMaxBytes+1))
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		hash := sha256.Sum256(content)
		digest := hex.EncodeToString(hash[:])
		integrityErr := validateVisualArtifactImage(content)
		if integrityErr == nil && (int64(len(content)) != version.SizeBytes ||
			!strings.EqualFold(digest, artifact.ContentSHA256) ||
			!strings.EqualFold(digest, version.ContentSHA256)) {
			integrityErr = errors.New("image byte size or immutable hash does not match")
		}
		if integrityErr != nil {
			return &sessionRunnerVisualArtifactValidationRequired{
				Artifacts: []string{artifact.Name + " (" + integrityErr.Error() + ")"},
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
