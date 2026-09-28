package server

import (
	"errors"
	"strings"

	transcriptstore "synon-go/internal/persistence/transcript"
)

// Read the same immutable produced selection that completion will publish.
// The current workspace inventory also contains unfinished attempts and inputs;
// neither can replace or invalidate the selected delivery's exact image bytes.
func (s *Server) sessionRunnerVisualDeliveryEvidence(projectID string, candidates []transcriptstore.ArtifactReferenceInput) ([]sessionReviewerArtifactEvidence, error) {
	var result []sessionReviewerArtifactEvidence
	for _, candidate := range latestSessionRunnerArtifactReferences(candidates) {
		if candidate.Relation != transcriptstore.ArtifactRelationProduced {
			continue
		}
		artifact, version, found, err := s.workspaceStore.GetArtifactVersionMetadata(candidate.VersionID)
		if err != nil {
			return nil, err
		}
		if !found || artifact.ID != candidate.ArtifactID || artifact.ProjectID != projectID {
			return nil, errors.New("visual delivery artifact is outside the selected project or version")
		}
		if !visualArtifactName(artifact.Name) {
			continue
		}
		retention, found, err := s.workspaceStore.ArtifactRetentionMode(artifact.ID)
		if err != nil {
			return nil, err
		}
		if !found || retention != "snapshot" {
			continue
		}
		result = append(result, sessionReviewerArtifactEvidence{
			ArtifactID: artifact.ID, VersionID: version.ID, Name: artifact.Name, Kind: artifact.Kind,
			ContentSHA256: strings.ToLower(strings.TrimSpace(version.ContentSHA256)),
		})
	}
	return result, nil
}
