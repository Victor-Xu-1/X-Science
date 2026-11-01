package workspace

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func resourceTagUTF16Length(value string) int {
	units := 0
	for _, char := range value {
		units++
		if char > 0xffff {
			units++
		}
	}
	return units
}

func resourceTagFields(name, icon, color string) (string, string, error) {
	if !utf8.ValidString(name) || resourceTagUTF16Length(name) > 64 {
		return "", "", ErrResourceTagInvalid
	}
	name, err := normalizeLiteratureCollectionText(name, 64, true)
	if err != nil {
		return "", "", ErrResourceTagInvalid
	}
	key := cases.Lower(language.Und).String(name)
	if resourceTagUTF16Length(key) > 64 {
		return "", "", ErrResourceTagInvalid
	}
	switch icon {
	case "tag", "star", "bookmark", "flask-conical", "book-open", "database", "code-2", "bot":
	default:
		return "", "", ErrResourceTagInvalid
	}
	switch color {
	case "gray", "red", "orange", "amber", "green", "blue", "purple", "pink":
	default:
		return "", "", ErrResourceTagInvalid
	}
	return name, key, nil
}

func ensureResourceTagState(ctx context.Context, tx *sql.Tx, owner string, now int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO resource_tag_catalog_state(owner_user_id) VALUES(?) ON CONFLICT(owner_user_id) DO NOTHING`, owner); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO resource_tags(owner_user_id,id,system_key,sort_order,created_at,updated_at)
 VALUES(?,?,'favorite',0,?,?) ON CONFLICT(owner_user_id,id) DO NOTHING`, owner, ResourceFavoriteTagID, now, now)
	return err
}

func requireResourceTagRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrResourceTagConflict
	}
	return nil
}

func (s *Store) mutateResourceTags(ctx context.Context, owner, operationID, action string, request any,
	apply func(*sql.Tx, int64) (string, bool, error),
) (ResourceTagReceipt, error) {
	var receipt ResourceTagReceipt
	if ctx == nil || !validLiteratureID(owner) || !validLiteratureID(operationID) {
		return receipt, ErrResourceTagInvalid
	}
	if s == nil || s.db == nil {
		return receipt, errors.New("workspace store is closed")
	}
	hash, err := mutationRequestHash(request)
	if err != nil {
		return receipt, err
	}
	operation := "resource.tag." + action
	if found, err := s.lookupMutationResult(ctx, owner, operationID, operation, hash, &receipt); err != nil || found {
		return receipt, err
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if found, err := lookupMutationResultTx(ctx, tx, owner, operationID, operation, hash, &receipt); err != nil || found {
			return err
		}
		now := s.now().UTC().UnixMilli()
		if now <= 0 || now > ResourceTagTimestampMax {
			return ErrResourceTagInvalid
		}
		if err := ensureResourceTagState(ctx, tx, owner, now); err != nil {
			return err
		}
		id, changed, err := apply(tx, now)
		if err != nil {
			return err
		}
		if changed {
			result, err := tx.ExecContext(ctx, `UPDATE resource_tag_catalog_state SET revision=revision+1 WHERE owner_user_id=?`, owner)
			if err != nil {
				return err
			}
			if err := requireResourceTagRow(result); err != nil {
				return err
			}
		}
		snapshot, err := readResourceTagSnapshot(ctx, tx, owner)
		if err != nil {
			return err
		}
		receipt = ResourceTagReceipt{Schema: resourceTagReceiptSchema, OperationID: operationID, Action: action, TagID: id, Snapshot: snapshot}
		return insertMutationResultTx(ctx, tx, owner, operationID, operation, hash, receipt, s.now().UTC())
	})
	if err != nil {
		if strings.Contains(err.Error(), "resource_tags.owner_user_id, resource_tags.name_key") {
			return ResourceTagReceipt{}, ErrResourceTagNameConflict
		}
		return ResourceTagReceipt{}, err
	}
	return receipt, nil
}

func (s *Store) CreateResourceTag(ctx context.Context, owner string, input ResourceTagCreate) (ResourceTagReceipt, error) {
	return s.mutateResourceTags(ctx, owner, input.OperationID, "create", input, func(tx *sql.Tx, now int64) (string, bool, error) {
		name, key, err := resourceTagFields(input.Name, input.IconKey, input.ColorKey)
		if err != nil {
			return "", false, err
		}
		var order int64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(sort_order) FROM resource_tags WHERE owner_user_id=?`, owner).Scan(&order); err != nil {
			return "", false, err
		}
		if order == math.MaxInt64 {
			return "", false, ErrResourceTagConflict
		}
		id := uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO resource_tags(owner_user_id,id,name,name_key,icon_key,color_key,sort_order,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?)`, owner, id, name, key, input.IconKey, input.ColorKey, order+1, now, now)
		return id, err == nil, err
	})
}

func (s *Store) UpdateResourceTag(ctx context.Context, owner, id string, input ResourceTagUpdate) (ResourceTagReceipt, error) {
	if !validLiteratureID(id) {
		return ResourceTagReceipt{}, ErrResourceTagInvalid
	}
	request := struct {
		ID    string
		Input ResourceTagUpdate
	}{id, input}
	return s.mutateResourceTags(ctx, owner, input.OperationID, "update", request, func(tx *sql.Tx, now int64) (string, bool, error) {
		previous, err := readResourceTag(ctx, tx, owner, id)
		if err != nil {
			return "", false, err
		}
		if previous.SystemKey != "" {
			return "", false, ErrResourceTagProtected
		}
		if input.ExpectedUpdatedAt <= 0 || input.ExpectedUpdatedAt >= ResourceTagTimestampMax {
			return "", false, ErrResourceTagInvalid
		}
		if previous.UpdatedAt != input.ExpectedUpdatedAt {
			return "", false, ErrResourceTagConflict
		}
		name, key, err := resourceTagFields(input.Name, input.IconKey, input.ColorKey)
		if err != nil {
			return "", false, err
		}
		if now <= previous.UpdatedAt {
			now = previous.UpdatedAt + 1
		}
		result, err := tx.ExecContext(ctx, `UPDATE resource_tags SET name=?,name_key=?,icon_key=?,color_key=?,updated_at=? WHERE owner_user_id=? AND id=? AND updated_at=?`,
			name, key, input.IconKey, input.ColorKey, now, owner, id, input.ExpectedUpdatedAt)
		if err == nil {
			err = requireResourceTagRow(result)
		}
		return id, err == nil, err
	})
}

func resourceTagCustomIDs(ctx context.Context, tx *sql.Tx, owner string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM resource_tags WHERE owner_user_id=? AND system_key IS NULL ORDER BY sort_order,id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func writeResourceTagOrder(ctx context.Context, tx *sql.Tx, owner string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var maximum int64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(sort_order) FROM resource_tags WHERE owner_user_id=?`, owner).Scan(&maximum); err != nil {
		return err
	}
	if maximum > (math.MaxInt64-1)/2 {
		return ErrResourceTagConflict
	}
	// All temporary positions are above the previous maximum and nonnegative.
	if _, err := tx.ExecContext(ctx, `UPDATE resource_tags SET sort_order=sort_order+? WHERE owner_user_id=? AND system_key IS NULL`, maximum+1, owner); err != nil {
		return err
	}
	for index, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE resource_tags SET sort_order=? WHERE owner_user_id=? AND id=? AND system_key IS NULL`, index+1, owner, id)
		if err != nil {
			return err
		}
		if err := requireResourceTagRow(result); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteResourceTag(ctx context.Context, owner, id, operationID string) (ResourceTagReceipt, error) {
	if !validLiteratureID(id) {
		return ResourceTagReceipt{}, ErrResourceTagInvalid
	}
	return s.mutateResourceTags(ctx, owner, operationID, "delete", id, func(tx *sql.Tx, _ int64) (string, bool, error) {
		previous, err := readResourceTag(ctx, tx, owner, id)
		if err != nil {
			return "", false, err
		}
		if previous.SystemKey != "" {
			return "", false, ErrResourceTagProtected
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM resource_tags WHERE owner_user_id=? AND id=?`, owner, id)
		if err != nil {
			return "", false, err
		}
		if err := requireResourceTagRow(result); err != nil {
			return "", false, err
		}
		ids, err := resourceTagCustomIDs(ctx, tx, owner)
		if err == nil {
			err = writeResourceTagOrder(ctx, tx, owner, ids)
		}
		return id, err == nil, err
	})
}

func (s *Store) ReorderResourceTags(ctx context.Context, owner string, input ResourceTagOrder) (ResourceTagReceipt, error) {
	if input.TagIDs == nil {
		return ResourceTagReceipt{}, ErrResourceTagInvalid
	}
	input.TagIDs = append([]string{}, input.TagIDs...)
	return s.mutateResourceTags(ctx, owner, input.OperationID, "reorder", input, func(tx *sql.Tx, _ int64) (string, bool, error) {
		previous, err := resourceTagCustomIDs(ctx, tx, owner)
		if err != nil {
			return "", false, err
		}
		if len(previous) != len(input.TagIDs) {
			return "", false, ErrResourceTagConflict
		}
		remaining := map[string]bool{}
		for _, id := range previous {
			remaining[id] = true
		}
		changed := false
		for index, id := range input.TagIDs {
			if !remaining[id] {
				return "", false, ErrResourceTagInvalid
			}
			delete(remaining, id)
			changed = changed || id != previous[index]
		}
		if !changed {
			return "", false, nil
		}
		return "", true, writeResourceTagOrder(ctx, tx, owner, input.TagIDs)
	})
}

func validResourceTagResourceID(value string) bool {
	return value != "" && utf8.ValidString(value) && resourceTagUTF16Length(value) <= 256
}
