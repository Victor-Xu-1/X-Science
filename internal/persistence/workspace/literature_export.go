package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

type LiteratureExportFormat string

const (
	LiteratureExportBibtex LiteratureExportFormat = "bibtex"
	LiteratureExportRIS    LiteratureExportFormat = "ris"
)

type LiteratureReferenceExport struct {
	Schema  string                 `json:"schema"`
	ItemIDs []string               `json:"item_ids"`
	Format  LiteratureExportFormat `json:"format"`
	Content string                 `json:"content"`
}

// ExportLiteratureReferences reads the original catalogue in one snapshot. No mutation,
// attachment access, provider call or partially formatted success is performed.
func (s *Store) ExportLiteratureReferences(ctx context.Context, owner string, ids []string, format LiteratureExportFormat) (LiteratureReferenceExport, error) {
	empty := LiteratureReferenceExport{}
	if !validLiteratureID(owner) || len(ids) == 0 || len(ids) > 200 || (format != LiteratureExportBibtex && format != LiteratureExportRIS) {
		return empty, ErrLiteratureInvalid
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validLiteratureID(id) || !utf8.ValidString(id) || seen[id] {
			return empty, ErrLiteratureInvalid
		}
		seen[id] = true
	}
	database := s.readDatabase()
	if database == nil {
		return empty, errors.New("workspace store is closed")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		item, err := readLiteratureItem(ctx, tx, owner, id)
		if err != nil {
			return empty, err
		}
		if item.Deleted {
			return empty, ErrLiteratureUnavailable
		}
		entry, err := literatureReferenceEntry(item, format)
		if err != nil {
			return empty, err
		}
		entries = append(entries, entry)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return LiteratureReferenceExport{Schema: "x-science.literature-export.v1", ItemIDs: append([]string(nil), ids...), Format: format, Content: strings.Join(entries, "\n\n") + "\n"}, nil
}

func literatureReferenceEntry(item LiteratureItem, format LiteratureExportFormat) (string, error) {
	if format == LiteratureExportRIS {
		return literatureRISEntry(item)
	}
	return literatureBibtexEntry(item)
}
