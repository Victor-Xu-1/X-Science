package server

import (
	"context"
	"errors"
	"strings"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Read only the already-authorized immutable handle. No accepted historical
// reading grant, mutable pathname, OCR worker or visual rendering is introduced.
func (s *Server) literatureStudyPDF(ctx context.Context, source workspace.LiteratureStudySource, owner string) (literatureStudyEvidence, error) {
	result := literatureStudyEvidence{Coverage: "passages"}
	expected := source.Version
	if expected == nil || expected.SizeBytes <= 0 || expected.SizeBytes > transcriptstore.MaxPDFReadingBytes {
		return result, errors.New("study PDF outside existing read bound")
	}
	item, version, reader, err := s.workspaceStore.OpenLiteratureVersionContent(ctx, owner, expected.ID, false)
	if err != nil {
		return result, err
	}
	defer reader.Close()
	if item != source.ItemID || version.AttachmentID != expected.AttachmentID || version.Checksum != expected.Checksum || version.SizeBytes != expected.SizeBytes {
		return result, workspace.ErrLiteratureStudySourceChanged
	}
	path, cleanup, err := stageAgentWorkspacePDFSource(ctx, reader, version.SizeBytes)
	if err != nil {
		return result, err
	}
	defer cleanup()
	scope, err := inspectAgentWorkspacePDFPageScope(ctx, path, nil)
	if err != nil {
		return result, err
	}
	count := scope.inspection.PageCount
	pages := []int{1}
	if count > 0 {
		pages = nil
		for page := 1; page <= count && page <= 4; page++ {
			pages = append(pages, page)
		}
	}
	view, err := readAgentWorkspacePDFTextWindow(ctx, path, version.Filename, version.MIMEType, version.SizeBytes, pages, map[string]any{"offset": 1, "limit": 500}, map[string]any{})
	if err != nil {
		return result, err
	}
	result.Text = strings.TrimSpace(stringValue(view["content"]))
	extraction, _ := view["text_extraction"].(map[string]any)
	textPages, _ := extraction["text_pages"].([]int)
	// The line window contains host page markers even for blank pages.
	// Only the original successful-body observer proves readable paper text.
	if extraction["status"] != "parsed" || len(textPages) == 0 || result.Text == "" {
		return result, errors.New("study PDF has no extractable body text")
	}
	result.VersionID = version.ID
	result.Pages = pages
	allPagesHaveText := len(textPages) == len(pages)
	for index, page := range pages {
		if !allPagesHaveText || textPages[index] != page {
			allPagesHaveText = false
			break
		}
	}
	if scope.reason == "" && count > 0 && count == len(pages) && allPagesHaveText && view["truncated"] != true && extraction["truncated"] == false {
		result.Coverage = "full-text"
	}
	return result, nil
}
