package server

import (
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

type runnerPDFReadingHint struct {
	BindingID     string `json:"binding_id"`
	DocumentCount int    `json:"document_count"`
	Active        bool   `json:"active"`
	PageNumber    int    `json:"page_number,omitempty"`
	PageCount     int    `json:"page_count"`
}

func runnerPDFReadingHints(message eventjournal.Message, refs []transcriptstore.UserArtifactReference) (map[string]runnerPDFReadingHint, error) {
	input, _ := message["inputData"].(map[string]any)
	value := input[transcriptstore.PDFReadingSnapshotKey]
	if value == nil {
		return nil, nil
	}
	if intent, _ := input["turn_intent"].(string); intent != "" {
		return nil, nil
	}
	snapshot, err := transcriptstore.DecodePDFReadingSnapshot(value)
	if err != nil {
		return nil, err
	}
	byVersion := map[string]transcriptstore.UserArtifactReference{}
	for _, ref := range refs {
		byVersion[ref.VersionID] = ref
	}
	active := snapshot.ActiveBindingID
	if active == "" && len(snapshot.Bindings) > 0 {
		active = snapshot.Bindings[0].BindingID
	}
	result := map[string]runnerPDFReadingHint{}
	for _, binding := range snapshot.Bindings {
		ref, found := byVersion[binding.VersionID]
		if !found || ref.ArtifactID != binding.ArtifactID || ref.Checksum != binding.Checksum || ref.SizeBytes != binding.SizeBytes {
			return nil, transcriptstore.ErrPDFReadingInvalid
		}
		hint := runnerPDFReadingHint{BindingID: binding.BindingID, DocumentCount: len(snapshot.Bindings), Active: binding.BindingID == active, PageCount: binding.PageCount}
		if binding.BindingID == snapshot.ActiveBindingID {
			hint.PageNumber = snapshot.PageNumber
		}
		result[binding.VersionID] = hint
	}
	return result, nil
}
