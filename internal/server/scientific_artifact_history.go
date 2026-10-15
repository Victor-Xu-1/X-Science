package server

import (
	"context"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Missing capability cannot refute a prior invalid result. Legacy conditions
// do not name versions, so carry them forward only when a sealed original
// candidate proves the complete immutable selection is unchanged.
func (s *Server) reconcileScientificArtifactHistory(ctx context.Context, run *sessionRunnerChatRun, validation *scientificArtifactCompletionValidation, original *sessionRunnerCompletionRecoveryCandidate) (*sessionRunnerCompletionAdvisory, error) {
	if run == nil || validation == nil || len(validation.Unavailable) == 0 || len(validation.Failures) > 0 || run.CorrectionCondition == nil {
		return nil, nil
	}
	condition := run.CorrectionCondition
	if err := condition.Validate(); err != nil {
		return nil, err
	}
	if condition.Reference == nil || len(condition.Reference.InvalidScientificArtifacts) == 0 {
		return nil, nil
	}
	unknown := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryChemicalHistory,
		Findings: []string{condition.ContentID() + ":historical_selection_unverified"}}
	if s == nil || s.transcriptStore == nil || s.workspaceStore == nil || run.Transcript == nil {
		return unknown, nil
	}
	if original == nil {
		candidate, found, err := s.loadTranscriptCompletionRecoveryCandidate(ctx, run.Transcript)
		if err != nil {
			return nil, err
		}
		if !found {
			return unknown, nil
		}
		original = &candidate
	}
	proof := original.ScientificSelectionSource
	if proof == nil || original.SourceRun == nil || proof.CorrectionCondition == nil || proof.CorrectionCondition.ContentID() != condition.ContentID() {
		return unknown, nil
	}
	sealed, complete, err := s.transcriptStore.SealedFailedArtifactCommitSnapshot(ctx, *proof)
	if err != nil {
		return nil, err
	}
	if !complete {
		return unknown, nil
	}
	potentialCommits := make([]transcriptstore.ArtifactReferenceInput, 0, len(sealed.Snapshot.References))
	for _, ref := range sealed.Snapshot.References {
		if ref.Availability != transcriptstore.ArtifactAvailable {
			// A missing historical member cannot disappear from the comparison
			// and make a smaller remaining selection appear unchanged.
			return unknown, nil
		}
		potentialCommits = append(potentialCommits, transcriptstore.ArtifactReferenceInput{
			ArtifactID: ref.ArtifactID, VersionID: ref.VersionID, Relation: ref.Relation,
		})
	}
	for _, ref := range sealed.InputReferences {
		if ref.Availability != transcriptstore.ArtifactAvailable {
			return unknown, nil
		}
	}
	commits, err := s.sessionRunnerArtifactReferencesFromSnapshot(sealed.Snapshot.References)
	if err != nil {
		return nil, err
	}
	content, _ := normalizeSessionRunnerArtifactReferencesToCurrentVersions(original.Content, commits)
	if content == "" {
		content = original.Content
	}
	selected, err := s.sessionRunnerArtifactReferencesForRound(original.SourceRun, commits, content, sealed.InputReferences)
	if err != nil {
		return nil, err
	}
	projectID := run.Transcript.Stream.ProjectID
	prior, complete, err := s.scientificArtifactSelectionIdentities(projectID, selected, content)
	if err != nil || !complete {
		return unknown, err
	}
	// Older continuation metadata may not identify every implied historical
	// deliverable. If the canonical selector omitted molecular heads, the old
	// complete validation scope is not recoverable; do not guess it.
	potential, complete, err := s.scientificArtifactSelectionIdentities(projectID, potentialCommits, content)
	if err != nil || !complete {
		return unknown, err
	}
	if !sameScientificArtifactSelection(prior, potential) || !sameScientificArtifactSelection(prior, validation.Selection) ||
		!scientificHistoryOutcomeApplies(validation.Selection, condition.Reference.InvalidScientificArtifacts) {
		return unknown, nil
	}
	validation.Failures = append(validation.Failures, condition.Reference.InvalidScientificArtifacts...)
	return nil, nil
}

func (s *Server) scientificArtifactSelectionIdentities(projectID string, commits []transcriptstore.ArtifactReferenceInput, content string) ([]scientificArtifactIdentity, bool, error) {
	versions, produced, _ := selectScientificArtifactVersions(commits, content)
	result := make([]scientificArtifactIdentity, 0, len(versions))
	for _, versionID := range versions {
		artifact, version, found, err := s.workspaceStore.GetArtifactVersionMetadata(versionID)
		if err != nil || !found {
			return nil, false, err
		}
		if artifact.ProjectID != projectID || version.ID != versionID || version.ArtifactID != artifact.ID ||
			(produced[versionID] != "" && produced[versionID] != artifact.ID) {
			return nil, false, errors.New("historical scientific artifact identity is unavailable")
		}
		format := scientificArtifactFormat(artifact.Name)
		if format == "" {
			// Names are mutable. Dropping this member would falsely shrink
			// both historical sets after a rename and reattribute its failure.
			return nil, false, nil
		}
		if strings.TrimSpace(version.ContentSHA256) == "" {
			return nil, false, nil
		}
		result = append(result, scientificArtifactIdentity{ArtifactID: artifact.ID, VersionID: version.ID, SHA256: version.ContentSHA256, Format: format})
	}
	return result, true, nil
}

// The old host outcome has exact parser codes, not per-file format records.
// Its existing closed code contract can preserve only a set-level failure:
// every unchanged member must currently share the compatible format. Mixed,
// unsupported or aggregate findings are unknown. Never parse display text or
// infer which individual file was invalid from its current name.
func scientificHistoryOutcomeApplies(selection []scientificArtifactIdentity, codes []string) bool {
	if len(selection) == 0 || len(codes) == 0 || selection[0].Format == "" {
		return false
	}
	format := selection[0].Format
	for _, identity := range selection {
		if identity.Format != format {
			return false
		}
	}
	for _, code := range codes {
		if !scientificArtifactDataFailureCode(format, code) {
			return false
		}
	}
	return true
}

func sameScientificArtifactSelection(left, right []scientificArtifactIdentity) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	set := make(map[scientificArtifactIdentity]struct{}, len(left))
	for _, identity := range left {
		set[identity] = struct{}{}
	}
	if len(set) != len(left) {
		return false
	}
	for _, identity := range right {
		if _, found := set[identity]; !found {
			return false
		}
		delete(set, identity)
	}
	return len(set) == 0
}
