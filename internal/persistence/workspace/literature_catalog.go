package workspace

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Store) GetLiteratureItem(ctx context.Context, owner, id string) (LiteratureItem, error) {
	return readLiteratureItem(ctx, s.readDatabase(), owner, id)
}

type literatureItemQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// A mutation reads the complete receipt through its own transaction, never a
// second connection or a pre-mutation mixture of metadata and memberships.
func readLiteratureItem(ctx context.Context, query literatureItemQuery, owner, id string) (LiteratureItem, error) {
	item := LiteratureItem{ProjectIDs: []string{}, Attachments: []LiteratureAttachment{}}
	var raw string
	var deleted sql.NullString
	err := query.QueryRowContext(ctx, `SELECT id,title,metadata_revision,metadata_json,deleted_at
	 FROM literature_items WHERE id=? AND owner_user_id=?`, id, owner).Scan(&item.ID, &item.Title, &item.MetadataRevision, &raw, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrLiteratureUnavailable
	}
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal([]byte(raw), &item.Metadata); err != nil {
		return item, err
	}
	item.Deleted = deleted.Valid
	rows, err := query.QueryContext(ctx, `SELECT project_id FROM project_literature WHERE item_id=? ORDER BY project_id`, id)
	if err != nil {
		return item, err
	}
	for rows.Next() {
		var project string
		if err = rows.Scan(&project); err != nil {
			break
		}
		item.ProjectIDs = append(item.ProjectIDs, project)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return item, err
	}
	rows, err = query.QueryContext(ctx, `SELECT a.id,a.kind,v.id,v.version_number,v.filename,v.mime_type,v.size_bytes,v.checksum,v.page_count,v.storage_path,
	 (v.content_available AND NOT EXISTS(SELECT 1 FROM blob_commit_markers b WHERE b.kind='literature-attachment-version' AND b.aggregate_id=v.id))
	 FROM literature_attachments a JOIN literature_attachment_versions v ON v.attachment_id=a.id WHERE a.item_id=? ORDER BY a.id,v.version_number DESC`, id)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	indexes := map[string]int{}
	for rows.Next() {
		var v LiteratureVersion
		var kind string
		if err = rows.Scan(&v.AttachmentID, &kind, &v.ID, &v.VersionNumber, &v.Filename, &v.MIMEType, &v.SizeBytes, &v.Checksum, &v.PageCount, &v.StoragePath, &v.Available); err != nil {
			return item, err
		}
		index, ok := indexes[v.AttachmentID]
		if !ok {
			index = len(item.Attachments)
			indexes[v.AttachmentID] = index
			item.Attachments = append(item.Attachments, LiteratureAttachment{ID: v.AttachmentID, Kind: kind, Versions: []LiteratureVersion{}})
		}
		item.Attachments[index].Versions = append(item.Attachments[index].Versions, v)
	}
	return item, rows.Err()
}

// Cursor binds the query and optional membership scope, not only a row offset.
type literatureCursor struct{ After, Query, Project, Owner string }

func (s *Store) ListLiteratureItems(ctx context.Context, owner, query, project, cursor string, limit int) (LiteraturePage, error) {
	page := LiteraturePage{Schema: "x-science.literature-page.v1", Items: []LiteratureItem{}}
	if !validLiteratureID(owner) || len(query) > 512 || limit < 1 || limit > 100 || len(cursor) > 4096 {
		return page, ErrLiteratureInvalid
	}
	bound := literatureCursor{Query: query, Project: project, Owner: owner}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &bound) != nil || bound.Query != query || bound.Project != project || bound.Owner != owner {
			return page, ErrLiteratureInvalid
		}
	}
	rows, err := s.readDatabase().QueryContext(ctx, `SELECT i.id FROM literature_items i WHERE i.owner_user_id=? AND i.deleted_at IS NULL AND i.id>?
	 AND (?='' OR instr(lower(i.title),lower(?))>0) AND (?='' OR EXISTS(SELECT 1 FROM project_literature p JOIN projects project ON project.id=p.project_id WHERE p.item_id=i.id AND p.project_id=? AND project.user_id=?)) ORDER BY i.id LIMIT ?`, owner, bound.After, query, query, project, project, owner, limit+1)
	if err != nil {
		return page, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return page, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
		bound.After = ids[len(ids)-1]
		raw, _ := json.Marshal(bound)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, id := range ids {
		item, err := s.GetLiteratureItem(ctx, owner, id)
		if err != nil {
			return page, err
		}
		if !item.Deleted {
			page.Items = append(page.Items, item)
		}
	}
	return page, nil
}

func (s *Store) PatchLiteratureItem(ctx context.Context, owner, id string, input LiteraturePatch) (LiteratureReceipt, error) {
	var receipt LiteratureReceipt
	if !validLiteratureID(owner) || !validLiteratureID(id) || !validLiteratureID(input.OperationID) || input.ExpectedRevision < 1 {
		return receipt, ErrLiteratureInvalid
	}
	metadata, err := literatureMetadata(input.Metadata)
	if err != nil {
		return receipt, err
	}
	if input.Title != nil && (strings.TrimSpace(*input.Title) == "" || len(*input.Title) > 4096) {
		return receipt, ErrLiteratureInvalid
	}
	hash, err := mutationRequestHash(struct {
		ID    string
		Input LiteraturePatch
	}{id, input})
	if err != nil {
		return receipt, err
	}
	if found, err := s.lookupMutationResult(ctx, owner, input.OperationID, "literature.patch", hash, &receipt); err != nil || found {
		return receipt, err
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		found, err := lookupMutationResultTx(ctx, tx, owner, input.OperationID, "literature.patch", hash, &receipt)
		if err != nil || found {
			return err
		}
		var revision int64
		var title, raw string
		var deleted sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT metadata_revision,title,metadata_json,deleted_at FROM literature_items WHERE id=? AND owner_user_id=?`, id, owner).Scan(&revision, &title, &raw, &deleted)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLiteratureUnavailable
		}
		if err != nil {
			return err
		}
		if revision != input.ExpectedRevision {
			return ErrLiteratureConflict
		}
		if input.Title != nil {
			title = strings.TrimSpace(*input.Title)
		}
		if input.Metadata != nil {
			raw = metadata
		}
		if input.Deleted != nil {
			deleted = sql.NullString{String: s.now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Valid: *input.Deleted}
		}
		_, err = tx.ExecContext(ctx, `UPDATE literature_items SET title=?,metadata_json=?,metadata_revision=metadata_revision+1,deleted_at=?,updated_at=? WHERE id=? AND owner_user_id=?`, title, raw, deleted, s.now().UTC(), id, owner)
		if err != nil {
			return err
		}
		item, err := readLiteratureItem(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		receipt = LiteratureReceipt{Schema: "x-science.literature-receipt.v1", OperationID: input.OperationID, Item: item}
		return insertMutationResultTx(ctx, tx, owner, input.OperationID, "literature.patch", hash, receipt, s.now().UTC())
	})
	if err != nil {
		return receipt, err
	}
	return receipt, nil
}

func (s *Store) SetLiteratureProject(ctx context.Context, owner, id, project, operation string, included bool) (LiteratureReceipt, error) {
	var receipt LiteratureReceipt
	if !validLiteratureID(owner) || !validLiteratureID(id) || !validLiteratureID(project) || !validLiteratureID(operation) {
		return receipt, ErrLiteratureInvalid
	}
	hash, _ := mutationRequestHash([]any{id, project, included})
	if found, err := s.lookupMutationResult(ctx, owner, operation, "literature.membership", hash, &receipt); err != nil || found {
		return receipt, err
	}
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		found, err := lookupMutationResultTx(ctx, tx, owner, operation, "literature.membership", hash, &receipt)
		if err != nil || found {
			return err
		}
		var valid bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects p,literature_items i WHERE p.id=? AND p.user_id=? AND i.id=? AND i.owner_user_id=? AND i.deleted_at IS NULL)`, project, owner, id, owner).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrLiteratureUnavailable
		}
		if included {
			_, err = tx.ExecContext(ctx, `INSERT INTO project_literature(project_id,item_id,added_at) VALUES(?,?,?) ON CONFLICT(project_id,item_id) DO NOTHING`, project, id, s.now().UTC())
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM project_literature WHERE project_id=? AND item_id=?`, project, id)
		}
		if err != nil {
			return err
		}
		item, err := readLiteratureItem(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		receipt = LiteratureReceipt{Schema: "x-science.literature-receipt.v1", OperationID: operation, Item: item}
		return insertMutationResultTx(ctx, tx, owner, operation, "literature.membership", hash, receipt, s.now().UTC())
	})
	if err != nil {
		return receipt, err
	}
	return receipt, nil
}
