package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func runnerLibraryPDFBindings(message eventjournal.Message) ([]transcriptstore.PDFReadingBinding, error) {
	input, _ := message["inputData"].(map[string]any)
	if input[transcriptstore.PDFReadingSnapshotKey] == nil {
		return nil, nil
	}
	if intent, _ := input["turn_intent"].(string); intent != "" {
		return nil, nil
	}
	snapshot, err := transcriptstore.DecodePDFReadingSnapshot(input[transcriptstore.PDFReadingSnapshotKey])
	if err != nil {
		return nil, err
	}
	bindings := []transcriptstore.PDFReadingBinding{}
	for _, binding := range snapshot.Bindings {
		if binding.SourceKind == "literature-attachment-version" {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

func runnerLibraryAttachmentText(message eventjournal.Message) ([]string, error) {
	bindings, err := runnerLibraryPDFBindings(message)
	if err != nil {
		return nil, err
	}
	result := []string{}
	if len(bindings) == 0 {
		return result, nil
	}
	input, _ := message["inputData"].(map[string]any)
	snapshot, err := transcriptstore.DecodePDFReadingSnapshot(input[transcriptstore.PDFReadingSnapshotKey])
	if err != nil {
		return nil, err
	}
	active := snapshot.ActiveBindingID
	if active == "" && len(snapshot.Bindings) > 0 {
		active = snapshot.Bindings[0].BindingID
	}
	for _, binding := range bindings {
		format, confidence, _ := agentWorkspaceFormatHint(binding.Name, binding.MIMEType)
		hint := runnerPDFReadingHint{BindingID: binding.BindingID, DocumentCount: len(snapshot.Bindings), Active: binding.BindingID == active, PageCount: binding.PageCount}
		if binding.BindingID == snapshot.ActiveBindingID {
			hint.PageNumber = snapshot.PageNumber
		}
		raw, err := json.Marshal(map[string]any{"type": "attachment", "source_kind": binding.SourceKind, "item_id": binding.ItemID, "attachment_id": binding.AttachmentID,
			"version_id": binding.VersionID, "filename": binding.Name, "content_type": binding.MIMEType, "size_bytes": binding.SizeBytes, "sha256": binding.Checksum,
			"read_only": true, "materialization": "lazy_via_version_id", "reader_contract": agentWorkspaceFormatMetadata(format, confidence, "available", binding.Name),
			"pdf_reading_context": hint})
		if err != nil {
			return nil, err
		}
		result = append(result, string(raw))
	}
	return result, nil
}

func (s *Server) openAcceptedLibraryReadingVersion(ctx context.Context, identity *agentKernelContext, owner, id string) (agentWorkspaceReadableVersion, error) {
	if identity == nil || !strings.HasPrefix(id, "litv_") {
		return agentWorkspaceReadableVersion{}, errors.New("Library reading version unavailable")
	}
	binding, err := s.workspaceStore.AcceptedLiteratureReadingBinding(ctx, owner, identity.access.Frame.ID, id)
	if err != nil {
		return agentWorkspaceReadableVersion{}, err
	}
	item, version, reader, err := s.workspaceStore.OpenLiteratureVersionContent(ctx, owner, id, true)
	if err != nil {
		return agentWorkspaceReadableVersion{}, err
	}
	if item != binding.ItemID || version.AttachmentID != binding.AttachmentID || version.Checksum != binding.Checksum || version.SizeBytes != binding.SizeBytes {
		_ = reader.Close()
		return agentWorkspaceReadableVersion{}, errors.New("Library reading identity changed")
	}
	return agentWorkspaceReadableVersion{reader: reader, filename: version.Filename, contentType: version.MIMEType, sizeBytes: version.SizeBytes, versionID: version.ID, sha256: version.Checksum}, nil
}
