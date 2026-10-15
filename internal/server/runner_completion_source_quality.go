package server

import (
	"context"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

type sessionRunnerSourceQuality struct {
	Evidence          []agentruntime.Message
	Findings          []string
	MaterialsObserved bool
}

// Both fresh completion and immutable-candidate recovery read the same durable
// evidence. This performs no retrieval, model call or artifact mutation; a
// recovered result must not lose a limitation merely by taking a shorter path.
func (s *Server) sessionRunnerCompletionSourceQuality(
	ctx context.Context, projectID string, run *sessionRunnerChatRun,
	commits []transcriptstore.ArtifactReferenceInput, durableEvidence []agentruntime.Message,
) (sessionRunnerSourceQuality, error) {
	quality := sessionRunnerSourceQuality{Evidence: durableEvidence}
	if run != nil && run.Transcript != nil {
		materials, err := s.sessionRunnerResearchMaterials(ctx, run)
		if err != nil {
			return quality, err
		}
		if len(materials.Attempts) > 0 || len(materials.Receipts) > 0 {
			quality.MaterialsObserved = true
			quality.Evidence = researchQualifiedEvidenceMessages(durableEvidence, materials)
		}
	}
	sourceClaims, err := s.validateSessionRunnerSourceClaimEvidence(ctx, projectID, commits, quality.Evidence)
	if err != nil {
		return quality, err
	}
	evidenceDepth, err := s.validateSessionRunnerEvidenceRecordDepth(ctx, projectID, commits, quality.Evidence,
		run.trustedScientificReviewSignalsSnapshot(), sessionRunnerTaskIntent(run))
	if err != nil {
		return quality, err
	}
	quality.Findings = append(sourceClaims, evidenceDepth...)
	return quality, nil
}
