package workspace

import (
	"errors"
	"github.com/google/uuid"
)

const (
	LiteratureStudyBatchSchema      = "x-science.literature-study-batch.v1"
	LiteratureStudyBatchLimit       = 5
	LiteratureStudyBatchConcurrency = 2
	literatureStudyBatchBytes       = 200_000
)

var (
	ErrLiteratureStudyBatchConflict = errors.New("literature study batch changed")
	ErrLiteratureStudyBatchBusy     = errors.New("literature study batch request is active")
	ErrLiteratureStudyBatchClaim    = errors.New("literature study batch request expired")
)

type LiteratureStudyBatchRow struct {
	ItemID         string                  `json:"item_id"`
	Request        LiteratureStudyGenerate `json:"request"`
	State          string                  `json:"state"`
	Issue          string                  `json:"issue,omitempty"`
	ResultRevision int64                   `json:"result_revision,omitempty"`
	Reused         bool                    `json:"reused,omitempty"`
}
type LiteratureStudyBatch struct {
	ID               string                    `json:"id"`
	Revision         int64                     `json:"revision"`
	Paused           bool                      `json:"paused"`
	Rows             []LiteratureStudyBatchRow `json:"rows"`
	RequestExpiresAt int64                     `json:"request_expires_at,omitempty"`
}
type LiteratureStudyBatchView struct {
	Schema      string                `json:"schema"`
	Limit       int                   `json:"limit"`
	Concurrency int                   `json:"concurrency"`
	Batch       *LiteratureStudyBatch `json:"batch"`
}

func NewLiteratureStudyBatchView(batch *LiteratureStudyBatch) LiteratureStudyBatchView {
	return LiteratureStudyBatchView{LiteratureStudyBatchSchema, LiteratureStudyBatchLimit, LiteratureStudyBatchConcurrency, batch}
}

type LiteratureStudyBatchCommand struct {
	Action           string   `json:"action"`
	OperationID      string   `json:"operation_id,omitempty"`
	BatchID          string   `json:"batch_id,omitempty"`
	ExpectedRevision int64    `json:"expected_revision,omitempty"`
	Language         string   `json:"language,omitempty"`
	ItemIDs          []string `json:"item_ids,omitempty"`
	Rows             []int    `json:"rows,omitempty"`
}

func studyBatchUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}
func (c LiteratureStudyBatchCommand) valid() bool {
	if c.Action == "start" {
		if !studyBatchUUID(c.OperationID) || !ValidLiteratureStudyLanguage(c.Language) || len(c.ItemIDs) < 1 || len(c.ItemIDs) > LiteratureStudyBatchLimit || c.BatchID != "" || c.ExpectedRevision != 0 || len(c.Rows) != 0 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range c.ItemIDs {
			if !validLiteratureID(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	if !studyBatchUUID(c.BatchID) || c.Language != "" || len(c.ItemIDs) != 0 {
		return false
	}
	if c.Action == "advance" {
		if c.OperationID != "" || c.ExpectedRevision != 0 || len(c.Rows) < 1 || len(c.Rows) > LiteratureStudyBatchConcurrency {
			return false
		}
		seen := map[int]bool{}
		for _, row := range c.Rows {
			if row < 0 || row >= LiteratureStudyBatchLimit || seen[row] {
				return false
			}
			seen[row] = true
		}
		return true
	}
	return (c.Action == "pause" || c.Action == "resume" || c.Action == "retry") && studyBatchUUID(c.OperationID) && c.ExpectedRevision > 0 && c.ExpectedRevision < literatureStudyMaxRevision && len(c.Rows) == 0
}

// This private, finite request capability is never supplied by the browser.
type LiteratureStudyBatchClaim struct {
	BatchID, Token string
	Rows           []int
}
type literatureStudyBatchAck struct {
	BatchID  string `json:"batch_id"`
	Revision int64  `json:"revision"`
}
