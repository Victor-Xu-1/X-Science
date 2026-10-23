// Package literaturediscovery resolves related bibliographic records without
// acquiring PDFs or changing the caller's Library. The host owns authorization.
package literaturediscovery

import (
	"context"
	"errors"
	"net/http"
)

var ErrInvalid = errors.New("invalid literature related request")

// ErrReferenceUnavailable means the authorized seed is absent or deleted;
// storage/network failures must remain distinguishable from changed metadata.
var ErrReferenceUnavailable = errors.New("literature reference unavailable")

type Identifier struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}

type Record struct {
	PaperID        string       `json:"paper_id"`
	Title          string       `json:"title"`
	Abstract       string       `json:"abstract"`
	Authors        []string     `json:"authors"`
	Year           *int         `json:"year"`
	IssuedText     string       `json:"issued_text"`
	ContainerTitle string       `json:"container_title"`
	ItemType       string       `json:"item_type"`
	URL            string       `json:"url"`
	Identifiers    []Identifier `json:"identifiers"`
	Volume         string       `json:"volume"`
	Pages          string       `json:"pages"`
}

type Request struct {
	ItemID                   string `json:"item_id"`
	ExpectedMetadataRevision int64  `json:"expected_metadata_revision"`
	Direction                string `json:"direction"`
	ResolutionToken          string `json:"resolution_token,omitempty"`
	Offset                   int    `json:"offset"`
}

type Result struct {
	Schema           string   `json:"schema"`
	ItemID           string   `json:"item_id"`
	MetadataRevision int64    `json:"metadata_revision"`
	Direction        string   `json:"direction"`
	State            string   `json:"state"`
	ResolutionToken  string   `json:"resolution_token,omitempty"`
	Seed             *Record  `json:"seed,omitempty"`
	Records          []Record `json:"records"`
	NextOffset       *int     `json:"next_offset,omitempty"`
	Issue            string   `json:"issue,omitempty"`
	RetryAt          int64    `json:"retry_at,omitempty"`
	Omitted          int      `json:"omitted,omitempty"`
}

type Reference struct {
	ID                string
	Revision          int64
	Title             string
	Identifiers       []Identifier
	SemanticScholarID string
}

type Catalog interface {
	GetReference(context.Context, string, string) (Reference, error)
}

type Options struct {
	Catalog      Catalog
	ClientForURL func(context.Context, string) (*http.Client, error)
	APIKey       func(context.Context, string) (string, error)
}
