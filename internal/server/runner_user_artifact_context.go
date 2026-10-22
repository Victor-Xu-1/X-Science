package server

import (
	"encoding/json"
	"strings"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

type runnerComposerAttachment struct {
	Type              string                `json:"type"`
	ID                string                `json:"id"`
	VersionID         string                `json:"version_id"`
	Filename          string                `json:"filename"`
	ArtifactRef       string                `json:"artifact_ref"`
	ContentType       string                `json:"content_type"`
	SizeBytes         int64                 `json:"size_bytes"`
	Checksum          string                `json:"sha256"`
	ReadOnly          bool                  `json:"read_only,omitempty"`
	Materialization   string                `json:"materialization"`
	ReaderContract    map[string]any        `json:"reader_contract"`
	PDFReadingContext *runnerPDFReadingHint `json:"pdf_reading_context,omitempty"`
}

func runnerModelMessageText(message eventjournal.Message) string {
	text := runnerMessageText(message)
	refs, err := decodeUserArtifactReferences(message["artifactRefs"])
	library, libraryErr := runnerLibraryAttachmentText(message)
	if err != nil || libraryErr != nil || len(refs) == 0 && len(library) == 0 {
		return text
	}
	attachments := append([]string(nil), library...)
	reading, err := runnerPDFReadingHints(message, refs)
	if err != nil {
		return text
	}
	for _, ref := range refs {
		format, confidence, found := agentWorkspaceFormatHint(ref.Filename, ref.ContentType)
		status := "available"
		if !found || format.Reader == "specialist_reader" {
			status = "reader_required"
		}
		var hint *runnerPDFReadingHint
		if value, found := reading[ref.VersionID]; found {
			copy := value
			hint = &copy
		}
		raw, err := json.Marshal(runnerComposerAttachment{
			Type: "attachment", ID: ref.ArtifactID, VersionID: ref.VersionID, Filename: ref.Filename,
			ArtifactRef: "{{artifact:" + ref.VersionID + "}}", ContentType: ref.ContentType, SizeBytes: ref.SizeBytes,
			Checksum: ref.Checksum, ReadOnly: true,
			Materialization:   "lazy_via_version_id",
			ReaderContract:    agentWorkspaceFormatMetadata(format, confidence, status, ref.Filename),
			PDFReadingContext: hint,
		})
		if err != nil {
			return text
		}
		attachments = append(attachments, string(raw))
	}
	if strings.TrimSpace(stringValue(message["messageContext"])) == "onboarding_first_task" {
		return "[System] Onboarding complete — first task chosen during setup.\n\n" + text +
			"\n\nThe attached onboarding-profile.md has the user's research background and the tools they enabled. " +
			"Start working on this now — they've already confirmed it; don't ask what they'd like to do or wait for a go-ahead.\n\n" +
			strings.Join(attachments, "\n")
	}
	return text + "\n\n" + strings.Join(attachments, "\n")
}

func validateRunnerUserArtifactContext(message eventjournal.Message) error {
	refs, err := decodeUserArtifactReferences(message["artifactRefs"])
	if err != nil {
		return err
	}
	if _, err := runnerPDFReadingHints(message, refs); err != nil {
		return err
	}
	contextType := strings.TrimSpace(stringValue(message["messageContext"]))
	if contextType == "" {
		return nil
	}
	if contextType == structuredOnboardingMessageContext {
		return nil
	}
	if contextType != "onboarding_first_task" || len(refs) == 0 || refs[0].Filename != "onboarding-profile.md" ||
		refs[0].ContentType != "text/markdown" {
		return transcriptstore.ErrEventConflict
	}
	return nil
}
