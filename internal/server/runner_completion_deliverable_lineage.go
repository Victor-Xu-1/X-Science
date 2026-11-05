package server

import (
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Select a historical output only when it covers a requirement that the
// current round and explicitly linked outputs do not already cover. Exact
// names are recovered before broad formats so a named file can satisfy both.
func (s *Server) retainSessionRunnerMissingDeliverables(
	run *sessionRunnerChatRun,
	commits []transcriptstore.ArtifactReferenceInput,
	currentVersions, retainedVersions map[string]struct{},
	hasDeliverySignal bool,
) error {
	requiredNames := sessionRunnerExplicitDeliverableNames(run.TaskIntent)
	requiredFormats := sessionRunnerExplicitDeliverableFormats(run.TaskIntent)
	// With no new or linked output, a generic continuation retains its
	// logical-task ledger instead of arbitrarily choosing one old file.
	genericOnly := hasDeliverySignal && len(requiredNames) == 0 && len(requiredFormats) == 0 &&
		sessionRunnerRequiresDurableArtifact(run.TaskIntent)
	if len(requiredNames) == 0 && len(requiredFormats) == 0 && !genericOnly {
		return nil
	}
	if run.Transcript == nil || s == nil || s.workspaceStore == nil {
		return nil
	}
	projectID := run.Transcript.Stream.ProjectID
	present := make(map[string]struct{})
	history := make([]transcriptstore.ArtifactReferenceInput, 0)
	for _, commit := range commits {
		version := strings.TrimSpace(commit.VersionID)
		if version == "" || commit.Relation != transcriptstore.ArtifactRelationProduced {
			continue
		}
		_, current := currentVersions[version]
		_, retained := retainedVersions[version]
		if !current && !retained {
			history = append(history, commit)
			continue
		}
		name, err := s.sessionRunnerSnapshotDeliverableName(projectID, commit)
		if err != nil {
			return err
		}
		if name != "" {
			if genericOnly {
				return nil
			}
			present[name] = struct{}{}
		}
	}
	missingNames := make(map[string]struct{})
	for _, name := range requiredNames {
		name = strings.ToLower(name)
		if _, covered := present[name]; !covered {
			missingNames[name] = struct{}{}
		}
	}
	missingFormats := make([]string, 0, len(requiredFormats))
	for _, format := range requiredFormats {
		if !sessionRunnerPresentHasDeliverableFormat(present, format) {
			missingFormats = append(missingFormats, format)
		}
	}
	if len(missingNames) == 0 && len(missingFormats) == 0 && !genericOnly {
		return nil
	}

	type candidate struct{ version, name string }
	formatCandidates := make(map[string]candidate, len(missingFormats))
	retain := func(output candidate) {
		retainedVersions[output.version] = struct{}{}
		present[output.name] = struct{}{}
	}
	// Walk the immutable ledger in reverse order for a stable preference
	// among equivalent historical candidates without retaining every match.
	for i := len(history) - 1; i >= 0; i-- {
		commit := history[i]
		name, err := s.sessionRunnerSnapshotDeliverableName(projectID, commit)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		output := candidate{version: strings.TrimSpace(commit.VersionID), name: name}
		if genericOnly {
			retain(output)
			return nil
		}
		if _, wanted := missingNames[name]; wanted {
			retain(output)
			delete(missingNames, name)
		}
		for _, format := range missingFormats {
			if _, exists := formatCandidates[format]; !exists && sessionRunnerArtifactMatchesDeliverableFormat(name, format) {
				formatCandidates[format] = output
			}
		}
		if len(missingNames) == 0 && len(formatCandidates) == len(missingFormats) {
			break
		}
	}
	for _, format := range missingFormats {
		if output, found := formatCandidates[format]; found && !sessionRunnerPresentHasDeliverableFormat(present, format) {
			retain(output)
		}
	}
	return nil
}
