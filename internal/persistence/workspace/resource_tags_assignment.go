package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

func (s *Store) SetResourceTagAssignment(ctx context.Context, owner string, input ResourceTagSetAssignment) (ResourceTagReceipt, error) {
	if !validLiteratureID(input.TagID) || input.ResourceType != "literature.item" || !utf8.ValidString(input.ResourceID) {
		return ResourceTagReceipt{}, ErrResourceTagInvalid
	}
	return s.mutateResourceTags(ctx, owner, input.OperationID, "assignment", input, func(tx *sql.Tx, now int64) (string, bool, error) {
		resourceID := strings.TrimFunc(input.ResourceID, literatureCollectionSpace)
		if !validResourceTagResourceID(resourceID) {
			return "", false, ErrResourceTagInvalid
		}
		if _, err := readResourceTag(ctx, tx, owner, input.TagID); err != nil {
			return "", false, err
		}
		if input.Assigned {
			var actualOwner string
			var deleted sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT owner_user_id,deleted_at FROM literature_items WHERE id=?`, resourceID).Scan(&actualOwner, &deleted)
			if errors.Is(err, sql.ErrNoRows) {
				return "", false, ErrResourceTagUnavailable
			}
			if err != nil {
				return "", false, err
			}
			if actualOwner != owner || deleted.Valid {
				return "", false, ErrResourceTagUnavailable
			}
		}
		var result sql.Result
		var err error
		if input.Assigned {
			result, err = tx.ExecContext(ctx, `INSERT INTO resource_tag_assignments(owner_user_id,tag_id,resource_type,resource_id,created_at)
 VALUES(?,?,?,?,?) ON CONFLICT(owner_user_id,tag_id,resource_type,resource_id) DO NOTHING`, owner, input.TagID, input.ResourceType, resourceID, now)
		} else {
			result, err = tx.ExecContext(ctx, `DELETE FROM resource_tag_assignments WHERE owner_user_id=? AND tag_id=? AND resource_type=? AND resource_id=?`, owner, input.TagID, input.ResourceType, resourceID)
		}
		if err != nil {
			return "", false, err
		}
		count, err := result.RowsAffected()
		return input.TagID, count != 0, err
	})
}
