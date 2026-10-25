package server

import (
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Read the same immutable produced selection that completion will publish.
// This metadata owner is shared by image integrity and scene scope; the mutable
// workspace inventory cannot replace the selected delivery's exact versions.
func (s *Server) sessionRunnerDeliveryEvidence(projectID string, candidates []transcriptstore.ArtifactReferenceInput) ([]sessionReviewerArtifactEvidence, error) {
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
		retention, found, err := s.workspaceStore.ArtifactRetentionMode(artifact.ID)
		if err != nil {
			return nil, err
		}
		if !found || retention != "snapshot" {
			continue
		}
		// Exact produced candidates may still be staged; storage errors must
		// not turn an unknown presentation state into accepted evidence.
		if _, _, err := s.workspaceStore.ArtifactVersionIntermediate(version.ID); err != nil {
			return nil, err
		}
		result = append(result, sessionReviewerArtifactEvidence{
			ArtifactID: artifact.ID, VersionID: version.ID, Name: artifact.Name, Kind: artifact.Kind,
			ContentSHA256: strings.ToLower(strings.TrimSpace(version.ContentSHA256)),
		})
	}
	return result, nil
}
