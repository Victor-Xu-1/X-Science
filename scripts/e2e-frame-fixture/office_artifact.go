package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	workspace "synon-go/internal/persistence/workspace"
)

// Only the non-packaged browser fixture accepts source bytes. Production
// artifact writes still go through the same Store blob/version authority.
type officeArtifactFixtureRequest struct {
	ArtifactID string   `json:"artifactId"`
	Versions   [][]byte `json:"versions"`
}

func validateOfficeArtifactFixture(request fixtureRequest) error {
	input := request.OfficeArtifact
	if request.FrameID == "" || len(request.FrameID) > fixtureIDLimit || input == nil ||
		input.ArtifactID == "" || input.ArtifactID != strings.TrimSpace(input.ArtifactID) ||
		len(input.ArtifactID) > fixtureIDLimit || strings.IndexFunc(input.ArtifactID, unicode.IsControl) >= 0 ||
		len(input.Versions) != 2 {
		return errors.New("seed-office-artifact requires bounded frameId, artifactId, and two PDF versions")
	}
	if request.Status != nil || request.Name != nil || request.TaskSummary != nil || request.StatusDescription != nil ||
		request.Model != nil || request.OutputData != nil || request.ContextData != nil ||
		request.ToolID != "" || len(request.Questions) != 0 || request.HistoryCount != 0 ||
		request.CutoverID != "" || request.Transcript != nil {
		return errors.New("seed-office-artifact does not accept other fixture fields")
	}
	for _, content := range input.Versions {
		if len(content) > 64*1024 || !bytes.HasPrefix(content, []byte("%PDF-")) {
			return errors.New("seed-office-artifact requires PDF sources of at most 64 KiB")
		}
	}
	return nil
}

func seedOfficeArtifactFixture(store *workspace.Store, request fixtureRequest) (map[string]any, error) {
	if err := validateOfficeArtifactFixture(request); err != nil {
		return nil, err
	}
	input := request.OfficeArtifact
	frame, found, err := store.GetFrameRealtimeContext(request.FrameID)
	if err != nil || !found {
		if err == nil {
			err = errors.New("office fixture frame is unavailable")
		}
		return nil, err
	}
	if _, found, err := store.GetArtifact(input.ArtifactID); err != nil {
		return nil, err
	} else if found {
		return nil, errors.New("office fixture artifact already exists")
	}
	const filename = "encoded-preview.pdf"
	versions := make([]string, 0, 2)
	parent := ""
	for index, content := range input.Versions {
		_, version, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
			ArtifactID: input.ArtifactID, ProjectID: frame.Frame.ProjectID, Name: filename,
			ContentType: "application/pdf", Content: bytes.NewReader(content), MaxBytes: 64 * 1024,
			CreatedBy: frame.UserID, ParentVersionID: parent, RootFrameID: frame.Frame.RootFrameID, FrameID: request.FrameID,
		})
		if err != nil {
			return nil, fmt.Errorf("write office fixture version %d: %w", index+1, err)
		}
		versions = append(versions, version.ID)
		parent = version.ID
	}
	return map[string]any{
		"ok": true, "artifactId": input.ArtifactID, "filename": filename,
		"versionId": versions[0], "latestVersionId": versions[1],
	}, nil
}
