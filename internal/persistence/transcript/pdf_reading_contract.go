package transcript

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	PDFReadingMetadataKey           = "_pdf_reading_context"
	PDFReadingSelectionKey          = "pdf_reading_selection"
	PDFReadingSnapshotKey           = "pdf_reading_context"
	PDFReadingContextSchema         = "x-science.pdf-reading-context.v1"
	PDFReadingSelectionSchema       = "x-science.pdf-reading-selection.v1"
	PDFReadingSnapshotSchema        = "x-science.pdf-reading-snapshot.v1"
	MaxPDFReadingBindings           = 3
	MaxPDFReadingBytes        int64 = 50 << 20
)

var (
	ErrPDFReadingInvalid     = errors.New("invalid PDF reading context")
	ErrPDFReadingScope       = errors.New("PDF reading scope changed")
	ErrPDFReadingStale       = errors.New("PDF reading context revision changed")
	ErrPDFReadingUnavailable = errors.New("PDF reading source unavailable")
	ErrPDFReadingLimit       = errors.New("PDF reading context limit exceeded")
	ErrPDFReadingState       = errors.New("stored PDF reading context is invalid")
)

type PDFReadingScope struct {
	FrameID                string `json:"frame_id"`
	FrameIncarnationID     string `json:"frame_incarnation_id"`
	RootFrameID            string `json:"root_frame_id"`
	RootFrameIncarnationID string `json:"root_frame_incarnation_id"`
	ProjectID              string `json:"project_id"`
}

func (scope PDFReadingScope) Valid() bool {
	for _, value := range []string{scope.FrameID, scope.FrameIncarnationID, scope.RootFrameID, scope.RootFrameIncarnationID, scope.ProjectID} {
		if value == "" || len(value) > 512 || value != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}

type PDFReadingBinding struct {
	BindingID    string `json:"binding_id"`
	SourceKind   string `json:"source_kind"`
	ArtifactID   string `json:"artifact_id,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	VersionID    string `json:"version_id"`
	Name         string `json:"name"`
	MIMEType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	Checksum     string `json:"checksum"`
	PageCount    int    `json:"page_count"`
	LinkedAt     string `json:"linked_at"`
}

type PDFReadingContext struct {
	Schema       string                   `json:"schema"`
	Scope        PDFReadingScope          `json:"scope"`
	Revision     int64                    `json:"revision"`
	Bindings     []PDFReadingBinding      `json:"bindings"`
	Changed      bool                     `json:"changed"`
	LastMutation *PDFReadingMutationProof `json:"-"`
}

// Only this reserved same-database record owns mutable session bindings.
type PDFReadingRecord struct {
	OwnerID      string                   `json:"owner_id"`
	Context      PDFReadingContext        `json:"context"`
	LastMutation *PDFReadingMutationProof `json:"last_mutation,omitempty"`
}

type PDFReadingMutationProof struct {
	OperationID      string `json:"operation_id"`
	Kind             string `json:"kind"`
	ExpectedRevision int64  `json:"expected_revision"`
	RequestSHA256    string `json:"request_sha256"`
}

type PDFReadingSource struct {
	SourceKind   string `json:"source_kind,omitempty"`
	ArtifactID   string `json:"artifact_id,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	VersionID    string `json:"version_id"`
}

type PDFReadingExpected struct {
	FrameIncarnationID     string `json:"expected_frame_incarnation_id"`
	RootFrameID            string `json:"expected_root_frame_id"`
	RootFrameIncarnationID string `json:"expected_root_frame_incarnation_id"`
	Revision               *int64 `json:"expected_revision"`
}

type PDFReadingSelection struct {
	Schema string `json:"schema"`
	PDFReadingExpected
	BindingIDs      []string `json:"binding_ids"`
	ActiveBindingID string   `json:"active_binding_id,omitempty"`
	PageNumber      int      `json:"page_number,omitempty"`
}

// Immutable message evidence, not a new live context owner or read-completion
// receipt. Queue acceptance freezes this exact value in the existing envelope.
type PDFReadingSnapshot struct {
	Schema          string              `json:"schema"`
	Scope           PDFReadingScope     `json:"scope"`
	Revision        int64               `json:"revision"`
	Bindings        []PDFReadingBinding `json:"bindings"`
	ActiveBindingID string              `json:"active_binding_id,omitempty"`
	PageNumber      int                 `json:"page_number,omitempty"`
}

func (expected PDFReadingExpected) Valid() bool {
	for _, value := range []string{expected.FrameIncarnationID, expected.RootFrameID, expected.RootFrameIncarnationID} {
		if len(value) > 512 || value != strings.TrimSpace(value) {
			return false
		}
	}
	return expected.FrameIncarnationID != "" && expected.RootFrameID != "" && expected.RootFrameIncarnationID != "" &&
		expected.Revision != nil && *expected.Revision >= 0 && *expected.Revision <= 9007199254740991
}

func (expected PDFReadingExpected) Matches(scope PDFReadingScope) bool {
	return expected.Valid() && expected.FrameIncarnationID == scope.FrameIncarnationID &&
		expected.RootFrameID == scope.RootFrameID && expected.RootFrameIncarnationID == scope.RootFrameIncarnationID
}

func (selection PDFReadingSelection) Validate() error {
	if selection.Schema != PDFReadingSelectionSchema || !selection.PDFReadingExpected.Valid() ||
		len(selection.BindingIDs) > MaxPDFReadingBindings || selection.PageNumber < 0 {
		return ErrPDFReadingInvalid
	}
	seen := map[string]bool{}
	for _, id := range selection.BindingIDs {
		if id == "" || len(id) > 512 || strings.TrimSpace(id) != id || seen[id] {
			return ErrPDFReadingInvalid
		}
		seen[id] = true
	}
	if selection.ActiveBindingID != "" && !seen[selection.ActiveBindingID] || selection.PageNumber > 0 && selection.ActiveBindingID == "" {
		return ErrPDFReadingInvalid
	}
	return nil
}

func (binding PDFReadingBinding) Valid() bool {
	checksum, err := hex.DecodeString(binding.Checksum)
	_, timeErr := time.Parse(time.RFC3339Nano, binding.LinkedAt)
	for _, value := range []string{binding.BindingID, binding.ArtifactID, binding.VersionID} {
		if value != strings.TrimSpace(value) {
			return false
		}
	}
	return binding.BindingID != "" && len(binding.BindingID) <= 512 &&
		(binding.SourceKind == "artifact-version" || binding.SourceKind == "literature-attachment-version") && binding.Source().Valid() &&
		binding.Name != "" && len(binding.Name) <= 4096 && binding.MIMEType == "application/pdf" &&
		binding.SizeBytes > 0 && binding.SizeBytes <= MaxPDFReadingBytes && err == nil && len(checksum) == 32 &&
		binding.Checksum == strings.ToLower(binding.Checksum) && binding.PageCount > 1 && timeErr == nil
}

func DecodePDFReadingSelection(value any) (*PDFReadingSelection, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	var selection PDFReadingSelection
	if err != nil || json.Unmarshal(raw, &selection) != nil || selection.Validate() != nil {
		return nil, ErrPDFReadingInvalid
	}
	return &selection, nil
}
