package workspace

import (
	"context"
	"database/sql"
	"strings"
)

// Personal reading state is independent of bibliographic metadata and PDF progress.
type LiteratureReadingStatus string

const (
	LiteratureUnread  LiteratureReadingStatus = "unread"
	LiteratureReading LiteratureReadingStatus = "reading"
	LiteratureRead    LiteratureReadingStatus = "read"
)

type LiteratureReadingStatusInput struct {
	OperationID   string                  `json:"operation_id"`
	ItemIDs       []string                `json:"item_ids"`
	ReadingStatus LiteratureReadingStatus `json:"reading_status"`
}

type LiteratureReadingStatusReceipt struct {
	Schema        string                  `json:"schema"`
	OperationID   string                  `json:"operation_id"`
	ItemIDs       []string                `json:"item_ids"`
	ReadingStatus LiteratureReadingStatus `json:"reading_status"`
}

func validLiteratureReadingStatus(status LiteratureReadingStatus) bool {
	return status == LiteratureUnread || status == LiteratureReading || status == LiteratureRead
}

func validateReadingStatusReceipt(receipt LiteratureReadingStatusReceipt, input LiteratureReadingStatusInput, targets []string) error {
	if receipt.Schema != "x-science.literature-reading-status-receipt.v1" || receipt.OperationID != input.OperationID ||
		receipt.ReadingStatus != input.ReadingStatus || len(receipt.ItemIDs) != len(targets) {
		return ErrLiteratureInvalid
	}
	for index, id := range targets {
		if receipt.ItemIDs[index] != id {
			return ErrLiteratureInvalid
		}
	}
	return nil
}

// SetLiteratureReadingStatus binds the original target list before normalizing
// execution targets. Any foreign/deleted target rolls back the entire batch.
func (s *Store) SetLiteratureReadingStatus(ctx context.Context, owner string, input LiteratureReadingStatusInput) (LiteratureReadingStatusReceipt, error) {
	var receipt LiteratureReadingStatusReceipt
	if !validLiteratureID(owner) || !validLiteratureID(input.OperationID) || !validLiteratureReadingStatus(input.ReadingStatus) || len(input.ItemIDs) < 1 || len(input.ItemIDs) > 200 {
		return receipt, ErrLiteratureInvalid
	}
	input.ItemIDs = append([]string(nil), input.ItemIDs...)
	targets := make([]string, 0, len(input.ItemIDs))
	seen := make(map[string]bool, len(input.ItemIDs))
	for _, id := range input.ItemIDs {
		if !validLiteratureID(id) {
			return receipt, ErrLiteratureInvalid
		}
		if !seen[id] {
			targets = append(targets, id)
			seen[id] = true
		}
	}
	hash, err := mutationRequestHash(struct {
		ItemIDs []string                `json:"item_ids"`
		Status  LiteratureReadingStatus `json:"reading_status"`
	}{input.ItemIDs, input.ReadingStatus})
	if err != nil {
		return receipt, err
	}
	const operation = "literature.reading-status"
	if found, err := s.lookupMutationResult(ctx, owner, input.OperationID, operation, hash, &receipt); err != nil || found {
		if err == nil {
			err = validateReadingStatusReceipt(receipt, input, targets)
		}
		return receipt, err
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		found, err := lookupMutationResultTx(ctx, tx, owner, input.OperationID, operation, hash, &receipt)
		if err != nil {
			return err
		}
		if found {
			return validateReadingStatusReceipt(receipt, input, targets)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(targets)), ",")
		where := `owner_user_id=? AND deleted_at IS NULL AND id IN (` + placeholders + `)`
		args := make([]any, 1, len(targets)+1)
		args[0] = owner
		for _, id := range targets {
			args = append(args, id)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM literature_items WHERE `+where, args...).Scan(&count); err != nil {
			return err
		}
		if count != len(targets) {
			return ErrLiteratureUnavailable
		}
		updateArgs := append([]any{input.ReadingStatus, s.now().UTC()}, args...)
		if _, err := tx.ExecContext(ctx, `UPDATE literature_items SET reading_status=?,updated_at=? WHERE `+where, updateArgs...); err != nil {
			return err
		}
		receipt = LiteratureReadingStatusReceipt{Schema: "x-science.literature-reading-status-receipt.v1", OperationID: input.OperationID, ItemIDs: append([]string(nil), targets...), ReadingStatus: input.ReadingStatus}
		return insertMutationResultTx(ctx, tx, owner, input.OperationID, operation, hash, receipt, s.now().UTC())
	})
	return receipt, err
}
