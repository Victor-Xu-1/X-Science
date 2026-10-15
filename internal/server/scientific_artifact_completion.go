package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

type scientificArtifactValidator func(context.Context, io.Reader, string) (scientificArtifactValidation, error)

func (s *Server) validateSessionRunnerScientificArtifacts(ctx context.Context, projectID string, commits []transcriptstore.ArtifactReferenceInput, content string) (scientificArtifactCompletionValidation, error) {
	var store *workspace.Store
	if s != nil {
		store = s.workspaceStore
	}
	return inspectScientificArtifactSelection(ctx, store, projectID, commits, content, s.validateMoleculeArtifact)
}

func inspectScientificArtifactSelection(ctx context.Context, store *workspace.Store, projectID string, commits []transcriptstore.ArtifactReferenceInput, content string, validate scientificArtifactValidator) (scientificArtifactCompletionValidation, error) {
	result := scientificArtifactCompletionValidation{}
	versionIDs, producedArtifactByVersion, explicitReferences := selectScientificArtifactVersions(commits, content)
	if len(versionIDs) == 0 {
		return result, nil
	}
	if store == nil || projectID == "" {
		return result, errors.New("runner scientific artifact authority is unavailable")
	}
	failures := []string{}
	moleculeSetCounts := map[string]map[string]int{}
	for _, versionID := range versionIDs {
		artifact, version, reader, found, err := store.OpenArtifactVersionContent(versionID)
		if err != nil {
			return result, err
		}
		if !found {
			if reader != nil {
				_ = reader.Close()
			}
			if _, explicit := explicitReferences[versionID]; explicit {
				return result, errors.New("runner scientific artifact version is unavailable")
			}
			artifactID := producedArtifactByVersion[versionID]
			_, artifactFound, lookupErr := store.GetArtifact(artifactID)
			if lookupErr != nil {
				return result, lookupErr
			}
			if artifactFound {
				return result, errors.New("runner scientific artifact version is unavailable")
			}
			continue
		}
		if artifact.ProjectID != projectID || version.ID != versionID || version.ArtifactID != artifact.ID ||
			(producedArtifactByVersion[versionID] != "" && producedArtifactByVersion[versionID] != artifact.ID) {
			_ = reader.Close()
			return result, errors.New("runner scientific artifact version is unavailable")
		}
		format := scientificArtifactFormat(artifact.Name)
		if format == "" {
			_ = reader.Close()
			continue
		}
		validation, validationErr := validateScientificArtifactBytes(ctx, reader, version, format, validate)
		closeErr := reader.Close()
		if closeErr != nil {
			return result, fmt.Errorf("close runner scientific artifact: %w", closeErr)
		}
		result.Selection = append(result.Selection, scientificArtifactIdentity{ArtifactID: artifact.ID, VersionID: version.ID, SHA256: version.ContentSHA256, Format: format})
		if validationErr != nil {
			if reason, optional := optionalScientificValidationReason(validationErr); optional {
				result.Unavailable = append(result.Unavailable, scientificArtifactUnavailableObservation{
					VersionID: versionID, SHA256: version.ContentSHA256, Reason: reason,
				})
				continue
			}
			var invalid *scientificArtifactValidationError
			if errors.As(validationErr, &invalid) {
				failures = append(failures, invalid.Code)
				continue
			}
			return result, validationErr
		}
		extension := strings.ToLower(filepath.Ext(strings.TrimSpace(artifact.Name)))
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimSpace(artifact.Name)), extension))
		if moleculeSetCounts[stem] == nil {
			moleculeSetCounts[stem] = map[string]int{}
		}
		moleculeSetCounts[stem][format] = validation.ParsedCount
	}
	failures = append(failures, moleculeSetRecordCountFailures(moleculeSetCounts)...)
	sort.Strings(failures)
	result.Failures = failures
	return result, nil
}

func scientificArtifactFormat(name string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".sdf":
		return "sdf"
	case ".smi", ".smiles":
		return "smi"
	default:
		return ""
	}
}

func selectScientificArtifactVersions(commits []transcriptstore.ArtifactReferenceInput, content string) ([]string, map[string]string, map[string]struct{}) {
	allowedVersions := map[string]struct{}{}
	latestProduced := map[string]string{}
	for _, commit := range commits {
		versionID := strings.TrimSpace(commit.VersionID)
		if versionID == "" {
			continue
		}
		allowedVersions[versionID] = struct{}{}
		if commit.Relation == transcriptstore.ArtifactRelationProduced && strings.TrimSpace(commit.ArtifactID) != "" {
			latestProduced[commit.ArtifactID] = versionID
		}
	}
	selected := map[string]struct{}{}
	producedArtifactByVersion := map[string]string{}
	for _, versionID := range latestProduced {
		selected[versionID] = struct{}{}
	}
	for artifactID, versionID := range latestProduced {
		producedArtifactByVersion[versionID] = artifactID
	}
	explicitReferences := map[string]struct{}{}
	for _, match := range artifactReferencePattern.FindAllStringSubmatch(content, -1) {
		if len(match) == 2 {
			versionID := strings.TrimSpace(match[1])
			if _, ok := allowedVersions[versionID]; ok {
				selected[versionID] = struct{}{}
				explicitReferences[versionID] = struct{}{}
			}
		}
	}
	versionIDs := make([]string, 0, len(selected))
	for versionID := range selected {
		versionIDs = append(versionIDs, versionID)
	}
	sort.Strings(versionIDs)
	return versionIDs, producedArtifactByVersion, explicitReferences
}

func moleculeSetRecordCountFailures(moleculeSetCounts map[string]map[string]int) []string {
	failures := []string{}
	for stem, counts := range moleculeSetCounts {
		sdfCount, hasSDF := counts["sdf"]
		smilesCount, hasSMILES := counts["smi"]
		if hasSDF && hasSMILES && sdfCount != smilesCount {
			failures = append(failures, fmt.Sprintf(
				"molecule_set_record_count_mismatch:%s sdf=%d smiles=%d", stem, sdfCount, smilesCount,
			))
		}
	}
	sort.Strings(failures)
	return failures
}
