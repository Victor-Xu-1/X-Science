package literaturediscovery

import "time"

// PublicationSnapshot is provider evidence, independent of editable metadata.
// Its DOI is the source identity; a title-only revision does not expire it.
type PublicationSnapshot struct {
	DOI       string              `json:"doi"`
	CheckedAt time.Time           `json:"checked_at"`
	Notices   []PublicationNotice `json:"notices"`
}

type PublicationNotice struct {
	DOI    string `json:"doi"`
	Type   string `json:"type"`
	Date   string `json:"date,omitempty"`
	Source string `json:"source"`
}

type PublicationIssue string

const (
	PublicationUnavailable     PublicationIssue = "unavailable"
	PublicationRateLimited     PublicationIssue = "rate-limited"
	PublicationTimeout         PublicationIssue = "timeout"
	PublicationResponseInvalid PublicationIssue = "response-invalid"
	PublicationNotFound        PublicationIssue = "not-found"
)
