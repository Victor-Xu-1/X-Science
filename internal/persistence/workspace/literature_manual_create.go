package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrLiteratureTrashConflict = errors.New("matching literature is in trash")

// LiteratureManualCreate is explicit user metadata, not a staged external source.
type LiteratureManualCreate struct {
	OperationID                string         `json:"operation_id"`
	Title                      string         `json:"title"`
	Metadata                   map[string]any `json:"metadata,omitempty"`
	ProjectID                  string         `json:"project_id,omitempty"`
	CollectionID               string         `json:"collection_id,omitempty"`
	ExpectedCollectionRevision int64          `json:"expected_collection_revision,omitempty"`
}

// findOrCreateLiteratureMetadataTx shares candidate acceptance's original item
// authority. A matching active item is reused without overwriting its metadata.
func (s *Store) findOrCreateLiteratureMetadataTx(ctx context.Context, tx *sql.Tx, owner, title string, metadata func() (string, error), ids map[string]string, sources ...literatureCandidateSource) (string, error) {
	item, err := findMatchingLibraryItem(ctx, tx, owner, ids, sources...)
	if err != nil || item != "" {
		return item, err
	}
	raw, err := metadata()
	if err != nil {
		return "", err
	}
	item = uuid.NewString()
	now := s.now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, item, owner, title, raw, now, now)
	return item, err
}

func matchingTrashedLiterature(ctx context.Context, tx *sql.Tx, owner string, ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT metadata_json FROM literature_items WHERE owner_user_id=? AND deleted_at IS NOT NULL`, owner)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		metadata, err := decodeLiteratureMetadataObject(raw)
		if err != nil {
			return err
		}
		matched, err := legacyCandidateIdentityMatch(ids, legacyCandidateIdentitySets(metadata), false)
		if err != nil {
			return err
		}
		if matched {
			return ErrLiteratureTrashConflict
		}
	}
	return rows.Err()
}

func (s *Store) CreateManualLiterature(ctx context.Context, owner string, input LiteratureManualCreate) (LiteratureReceipt, error) {
	var receipt LiteratureReceipt
	if !validLiteratureID(owner) || !validLiteratureID(input.OperationID) || strings.TrimSpace(input.Title) == "" || len(input.Title) > 4096 || (input.ProjectID != "" && !validLiteratureID(input.ProjectID)) {
		return receipt, ErrLiteratureInvalid
	}
	if input.CollectionID == "" && input.ExpectedCollectionRevision != 0 || input.CollectionID != "" && (!validLiteratureID(input.CollectionID) || input.ExpectedCollectionRevision < 1) {
		return receipt, ErrLiteratureInvalid
	}
	raw, err := literatureMetadata(input.Metadata)
	if err != nil {
		return receipt, err
	}
	// Freeze before hashing and admission. No caller-owned map can change the
	// request after the receipt identity has been chosen.
	frozen, err := decodeLiteratureMetadataObject(raw)
	if err != nil {
		return receipt, err
	}
	input.Metadata = frozen
	input.Title = strings.TrimSpace(input.Title)
	hash, err := mutationRequestHash(input)
	if err != nil {
		return receipt, err
	}
	const operation = "literature.manual-create"
	if found, err := s.lookupMutationResult(ctx, owner, input.OperationID, operation, hash, &receipt); err != nil || found {
		return receipt, err
	}
	ids, err := candidateIdentities(frozen)
	if err != nil {
		return receipt, err
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if found, err := lookupMutationResultTx(ctx, tx, owner, input.OperationID, operation, hash, &receipt); err != nil || found {
			return err
		}
		if input.ProjectID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=? AND user_id=?)`, input.ProjectID, owner).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return ErrLiteratureUnavailable
			}
		}
		if err := matchingTrashedLiterature(ctx, tx, owner, ids); err != nil {
			return err
		}
		if input.CollectionID != "" {
			collection, err := readLiteratureCollection(ctx, tx, owner, input.CollectionID)
			if err != nil {
				return err
			}
			if collection.Revision != input.ExpectedCollectionRevision {
				return ErrLiteratureConflict
			}
		}
		id, err := s.findOrCreateLiteratureMetadataTx(ctx, tx, owner, input.Title, func() (string, error) { return raw, nil }, ids)
		if err != nil {
			return err
		}
		if input.ProjectID != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO project_literature(project_id,item_id,added_at) VALUES(?,?,?) ON CONFLICT(project_id,item_id) DO NOTHING`, input.ProjectID, id, s.now().UTC()); err != nil {
				return err
			}
		}
		if input.CollectionID != "" {
			if err := s.linkLiteratureCollectionItemTx(ctx, tx, input.CollectionID, id); err != nil {
				return err
			}
		}
		item, err := readLiteratureItem(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		receipt = LiteratureReceipt{Schema: "x-science.literature-receipt.v1", OperationID: input.OperationID, Item: item}
		return insertMutationResultTx(ctx, tx, owner, input.OperationID, operation, hash, receipt, s.now().UTC())
	})
	if err != nil {
		return LiteratureReceipt{}, err
	}
	return receipt, nil
}
