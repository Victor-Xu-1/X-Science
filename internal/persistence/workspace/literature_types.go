package workspace

import (
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrLiteratureInvalid     = errors.New("invalid literature request")
	ErrLiteratureUnavailable = errors.New("literature source unavailable")
	ErrLiteratureConflict    = errors.New("literature revision changed")
)

type LiteratureVersion struct {
	ID            string `json:"id"`
	AttachmentID  string `json:"attachment_id"`
	VersionNumber int    `json:"version_number"`
	Filename      string `json:"filename"`
	MIMEType      string `json:"mime_type"`
	SizeBytes     int64  `json:"size_bytes"`
	Checksum      string `json:"checksum"`
	PageCount     int    `json:"page_count"`
	Available     bool   `json:"available"`
	StoragePath   string `json:"-"`
}

type LiteratureAttachment struct {
	ID       string              `json:"id"`
	Kind     string              `json:"kind"`
	Versions []LiteratureVersion `json:"versions"`
}

type LiteratureItem struct {
	ID               string                  `json:"id"`
	Title            string                  `json:"title"`
	MetadataRevision int64                   `json:"metadata_revision"`
	ReadingStatus    LiteratureReadingStatus `json:"reading_status,omitempty"`
	Metadata         map[string]any          `json:"metadata"`
	Deleted          bool                    `json:"deleted"`
	ProjectIDs       []string                `json:"project_ids"`
	Attachments      []LiteratureAttachment  `json:"attachments"`
}

type LiteraturePage struct {
	Schema     string           `json:"schema"`
	Items      []LiteratureItem `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	TotalCount *int64           `json:"total_count,omitempty"`
}

type LiteratureReceipt struct {
	Schema       string         `json:"schema"`
	OperationID  string         `json:"operation_id"`
	Item         LiteratureItem `json:"item"`
	AttachmentID string         `json:"attachment_id,omitempty"`
	VersionID    string         `json:"version_id,omitempty"`
}

type LiteratureImport struct {
	OperationID string         `json:"operation_id"`
	ArtifactID  string         `json:"artifact_id"`
	VersionID   string         `json:"version_id"`
	Title       string         `json:"title"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ProjectID   string         `json:"project_id,omitempty"`
	PageCount   int            `json:"-"`
}

type LiteraturePatch struct {
	OperationID      string          `json:"operation_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	Title            *string         `json:"title,omitempty"`
	Metadata         map[string]any  `json:"metadata,omitempty"`
	MetadataPatch    json.RawMessage `json:"metadata_patch,omitempty"`
	Deleted          *bool           `json:"deleted,omitempty"`
}

func validLiteratureID(value string) bool {
	return value != "" && len(value) <= 512 && value == strings.TrimSpace(value)
}

func literatureMetadata(value map[string]any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 16384 {
		return "", ErrLiteratureInvalid
	}
	return string(raw), nil
}
