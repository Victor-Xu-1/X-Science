package literaturediscovery

import (
	"context"
	"errors"
	"time"
)

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
	PublicationDOIRequired     PublicationIssue = "doi-required"
	PublicationUnavailable     PublicationIssue = "unavailable"
	PublicationRateLimited     PublicationIssue = "rate-limited"
	PublicationTimeout         PublicationIssue = "timeout"
	PublicationResponseInvalid PublicationIssue = "response-invalid"
	PublicationNotFound        PublicationIssue = "not-found"
)

const (
	PublicationResultSchema  = "x-science.literature-publication-check.v1"
	PublicationReceiptSchema = "x-science.literature-publication-receipt.v1"
)

// ErrPublicationPending leaves the original operation unresolved. A retry must
// keep its operation ID until the transaction or its durable receipt is known.
var ErrPublicationPending = errors.New("publication check is still running")

type PublicationScope struct {
	OwnerUserID              string
	ItemID                   string
	OperationID              string
	ExpectedMetadataRevision int64
}

type PublicationResult struct {
	Schema           string               `json:"schema"`
	ItemID           string               `json:"item_id"`
	MetadataRevision int64                `json:"metadata_revision"`
	DOI              string               `json:"doi,omitempty"`
	Snapshot         *PublicationSnapshot `json:"snapshot,omitempty"`
	Issue            PublicationIssue     `json:"issue,omitempty"`
}

type PublicationReceipt struct {
	Schema      string            `json:"schema"`
	OperationID string            `json:"operation_id"`
	Result      PublicationResult `json:"result"`
}

type PublicationPreparation struct {
	DOI    string
	Replay *PublicationReceipt
}

// PublicationCatalog is implemented by the existing workspace Store. Commit
// saves evidence and the exact original-operation receipt in one transaction.
type PublicationCatalog interface {
	PreparePublicationCheck(context.Context, PublicationScope) (PublicationPreparation, error)
	CommitPublicationCheck(context.Context, PublicationScope, string, *PublicationSnapshot, PublicationIssue) (PublicationReceipt, error)
}
