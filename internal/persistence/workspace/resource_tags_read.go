package workspace

import (
	"context"
	"database/sql"
	"errors"
)

const resourceTagSelect = `SELECT id,COALESCE(system_key,''),COALESCE(name,''),COALESCE(icon_key,''),COALESCE(color_key,''),created_at,updated_at FROM resource_tags`

func emptyResourceTagSnapshot() ResourceTagSnapshot {
	return ResourceTagSnapshot{Schema: resourceTagSnapshotSchema, ResourceTypes: []string{"literature.item"},
		Tags: []ResourceTag{}, Assignments: []ResourceTagAssignment{}}
}

func scanResourceTag(row interface{ Scan(...any) error }) (ResourceTag, error) {
	var tag ResourceTag
	err := row.Scan(&tag.ID, &tag.SystemKey, &tag.Name, &tag.IconKey, &tag.ColorKey, &tag.CreatedAt, &tag.UpdatedAt)
	return tag, err
}

func readResourceTag(ctx context.Context, tx *sql.Tx, owner, id string) (ResourceTag, error) {
	tag, err := scanResourceTag(tx.QueryRowContext(ctx, resourceTagSelect+` WHERE owner_user_id=? AND id=?`, owner, id))
	if errors.Is(err, sql.ErrNoRows) {
		return tag, ErrResourceTagUnavailable
	}
	return tag, err
}

func readResourceTagSnapshot(ctx context.Context, tx *sql.Tx, owner string) (ResourceTagSnapshot, error) {
	result := emptyResourceTagSnapshot()
	err := tx.QueryRowContext(ctx, `SELECT revision FROM resource_tag_catalog_state WHERE owner_user_id=?`, owner).Scan(&result.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		// A known system capability, not a persisted custom row. Reads never
		// initialize the account or manufacture a creation timestamp.
		result.Tags = append(result.Tags, ResourceTag{ID: ResourceFavoriteTagID, SystemKey: "favorite"})
		return result, nil
	}
	if err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, resourceTagSelect+` WHERE owner_user_id=? ORDER BY sort_order,id`, owner)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		tag, scanErr := scanResourceTag(rows)
		if scanErr != nil {
			_ = rows.Close()
			return result, scanErr
		}
		result.Tags = append(result.Tags, tag)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, err
	}
	if len(result.Tags) == 0 || result.Tags[0].ID != ResourceFavoriteTagID || result.Tags[0].SystemKey != "favorite" {
		return result, errors.New("resource tag catalog is missing its system identity")
	}
	// Keep Trash relationships, but do not expose another account's resources
	// if an identity is ever transferred. No read performs cleanup or pruning.
	rows, err = tx.QueryContext(ctx, `SELECT a.tag_id,a.resource_type,a.resource_id,a.created_at
 FROM resource_tag_assignments a JOIN literature_items i ON a.resource_type='literature.item' AND i.id=a.resource_id AND i.owner_user_id=a.owner_user_id
 WHERE a.owner_user_id=? ORDER BY a.created_at,a.tag_id,a.resource_type,a.resource_id`, owner)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var assignment ResourceTagAssignment
		if err := rows.Scan(&assignment.TagID, &assignment.ResourceType, &assignment.ResourceID, &assignment.CreatedAt); err != nil {
			return result, err
		}
		result.Assignments = append(result.Assignments, assignment)
	}
	return result, rows.Err()
}

func (s *Store) ListResourceTags(ctx context.Context, owner string) (ResourceTagSnapshot, error) {
	if ctx == nil || !validLiteratureID(owner) {
		return ResourceTagSnapshot{}, ErrResourceTagInvalid
	}
	if s.readDatabase() == nil {
		return ResourceTagSnapshot{}, errors.New("workspace store is closed")
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ResourceTagSnapshot{}, err
	}
	defer tx.Rollback()
	result, err := readResourceTagSnapshot(ctx, tx, owner)
	if err != nil {
		return ResourceTagSnapshot{}, err
	}
	return result, tx.Commit()
}
