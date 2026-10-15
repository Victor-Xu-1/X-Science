package server

import (
	"context"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
)

// Completion validation never asks the model to create a replacement
// candidate in the same execution unit. The only local normalization allowed
// before validation removes malformed artifact-link presentation while
// preserving its visible label; it never invents an artifact identity or
// changes scientific content.
const maxSessionRunnerArtifactReferenceRepairs = 0

func (s *Server) runSessionAgentWithArtifactReferenceRepair(
	ctx context.Context,
	session sessionstore.Session,
	engine agentruntime.Engine,
	request agentruntime.RunRequest,
	run *sessionRunnerChatRun,
	onPlanModeCandidateRejected sessionRunnerPlanModeCandidateRejected,
) (agentruntime.RunResult, error) {
	if request.ToolRoundBudget == nil {
		request.ToolRoundBudget = agentruntime.NewToolRoundBudget(request.MaxToolRounds)
	}
	candidateStart := len(request.Messages)
	result, err := s.runSessionAgentWithPlanMode(
		ctx, session, engine, request, run, onPlanModeCandidateRejected,
	)
	if err != nil {
		return result, err
	}
	artifactCommits, err := s.sessionRunnerArtifactCommitReferences(ctx, run)
	if err != nil {
		return result, err
	}
	// Final round attachments are projected from durable generation receipts
	// after terminal completion. Never append the cumulative project inventory
	// to answers or turn historical files into new deliverables.
	finalContent := result.FinalMessage.Content
	if canonical, changed := normalizeSessionRunnerArtifactReferencesToCurrentVersions(finalContent, artifactCommits); changed {
		finalContent = canonical
	}
	replaceSessionRunnerFinalMessageContent(&result, finalContent)
	validationCommits, err := s.sessionRunnerActiveArtifactCommitReferences(
		ctx, run, artifactCommits, result.FinalMessage.Content,
	)
	if err != nil {
		return result, err
	}
	missingRequiredDeliverables, err := s.sessionRunnerMissingRequiredDeliverables(session, run, artifactCommits)
	if err != nil {
		return result, err
	}
	malformedArtifactReferences := sessionRunnerArtifactReferenceSyntaxFailures(result.FinalMessage.Content)
	if len(malformedArtifactReferences) > 0 {
		if normalized, changed := normalizeSessionRunnerMalformedArtifactReferences(result.FinalMessage.Content); changed {
			replaceSessionRunnerFinalMessageContent(&result, normalized)
			malformedArtifactReferences = sessionRunnerArtifactReferenceSyntaxFailures(normalized)
		}
	}
	// Resolve against the normalized candidate. A malformed or non-canonical
	// presentation that was safely reduced to its visible label no longer names
	// an artifact and must not keep a stale unresolved count. Canonical but
	// unknown UUIDs remain present and still fail closed below.
	unresolvedReferences, err := s.unresolvedSessionRunnerArtifactReferences(
		session, run, artifactCommits, result.FinalMessage.Content,
	)
	if err != nil {
		return result, err
	}
	unresolved := len(unresolvedReferences)
	missingLocalArtifacts, err := s.sessionRunnerMissingLocalArtifactDependencies(
		sessionRunnerProjectID(session), run, validationCommits, result.FinalMessage.Content,
	)
	if err != nil {
		return result, err
	}
	artifactCandidates, err := s.sessionRunnerCompletionArtifactCandidateReferences(
		session, run, validationCommits, result.FinalMessage.Content,
	)
	if err != nil {
		return result, err
	}
	durableEvidence, err := s.sessionRunnerDurableEvidenceMessages(ctx, run)
	if err != nil {
		return result, err
	}
	sourceQuality, err := s.sessionRunnerCompletionSourceQuality(
		ctx, sessionRunnerProjectID(session), run, validationCommits, durableEvidence,
	)
	if err != nil {
		return result, err
	}
	evidencePrefix := sessionRunnerDurableEvidencePrefix(sourceQuality.Evidence, result.Messages)
	evidenceMessages := make([]agentruntime.Message, 0, len(evidencePrefix)+len(result.Messages))
	evidenceMessages = append(evidenceMessages, evidencePrefix...)
	evidenceMessages = append(evidenceMessages, result.Messages...)
	unsupportedCitationRefs := []string(nil)
	if sourceQuality.MaterialsObserved || s.sessionRunnerSourceWorkflowActive(run) || sessionRunnerCitationIntegrityRequired(run) {
		unsupportedCitationRefs = unsupportedSessionRunnerCitationReferencesWithArtifactCandidatesUsing(
			evidenceMessages, candidateStart+len(evidencePrefix), result.FinalMessage.Content, nil, artifactCandidates,
			s.sessionRunnerEvidenceTool,
		)
	}
	sessionRunnerObserveUnsupportedCitationAdvisories(unsupportedCitationRefs)
	crossArtifactFailures, err := s.validateSessionRunnerCrossArtifactConsistency(
		ctx, sessionRunnerProjectID(session), validationCommits, result.FinalMessage.Content,
	)
	if err != nil {
		return result, err
	}
	crossArtifactFailures = append(
		crossArtifactFailures,
		sessionRunnerArtifactPublicationFreshnessFailures(result.Messages, result.FinalMessage.Content)...,
	)
	crossArtifactFailures = append(crossArtifactFailures, sourceQuality.Findings...)
	scientificFailures, err := s.validateSessionRunnerScientificArtifacts(ctx, sessionRunnerProjectID(session), validationCommits, result.FinalMessage.Content)
	if err != nil {
		return result, err
	}
	researchValidation, err := s.validateSessionRunnerResearchArtifactSelection(ctx, sessionRunnerProjectID(session), validationCommits)
	if err != nil {
		return result, err
	}
	artifactCandidates, err = s.sessionRunnerCompletionArtifactCandidateReferencesWithSelection(
		session, run, validationCommits, result.FinalMessage.Content,
		researchValidation.SelectedVersions, researchValidation.ManifestFound,
	)
	if err != nil {
		return result, err
	}
	fatalCrossArtifactFailures := sessionRunnerFatalCrossArtifactFailures(run, crossArtifactFailures)
	if unresolved == 0 && len(malformedArtifactReferences) == 0 && len(artifactCandidates.contractFailures) == 0 &&
		len(missingLocalArtifacts) == 0 && len(fatalCrossArtifactFailures) == 0 && len(scientificFailures) == 0 &&
		len(researchValidation.Failures) == 0 && len(missingRequiredDeliverables) == 0 {
		advisory, err := s.verifySessionRunnerVisualArtifactEvidence(session, validationCommits)
		if err != nil {
			return result, err
		}
		if err := s.publishSessionRunnerCompletionArtifacts(
			ctx, session, run, validationCommits,
		); err != nil {
			return result, err
		}
		language := ""
		if run != nil {
			language = run.ResponseLanguage
		}
		replaceSessionRunnerFinalMessageContent(&result,
			appendSessionRunnerCompletionAdvisories(result.FinalMessage.Content, language, advisory,
				sessionRunnerCompletionQualityAdvisory(run, crossArtifactFailures, unsupportedCitationRefs)))
		return result, nil
	}
	return result, &sessionRunnerReferenceIntegrityError{
		UnresolvedArtifacts:          unresolved,
		UnresolvedArtifactReferences: append([]string(nil), unresolvedReferences...),
		MalformedArtifactReferences:  malformedArtifactReferences,
		UnsupportedCitations:         nil,
		InvalidReferenceArtifacts:    append([]string(nil), artifactCandidates.contractFailures...),
		InvalidScientificArtifacts:   scientificFailures,
		CrossArtifactFailures:        fatalCrossArtifactFailures,
		InvalidResearchArtifacts:     researchValidation.Failures,
		MissingLocalArtifacts:        missingLocalArtifacts,
		MissingRequiredDeliverables:  missingRequiredDeliverables,
	}
}
