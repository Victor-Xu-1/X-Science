package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Evidence identity only. The Store remains the sole owner/access authority.
type LiteratureStudySource struct {
	ItemID   string
	Key      string
	Title    string
	Abstract string
	Version  *LiteratureVersion
}

func literatureStudySource(item LiteratureItem) LiteratureStudySource {
	source := LiteratureStudySource{ItemID: item.ID, Title: item.Title}
	source.Abstract, _ = item.Metadata["abstract"].(string)
	source.Abstract = strings.TrimSpace(source.Abstract)
	attachments := append([]LiteratureAttachment(nil), item.Attachments...)
	sort.Slice(attachments, func(i, j int) bool { return attachments[i].ID < attachments[j].ID })
	for _, attachment := range attachments {
		if attachment.Kind != "fullText" {
			continue
		}
		var latest *LiteratureVersion
		for _, value := range attachment.Versions {
			value := value
			if latest == nil || value.VersionNumber > latest.VersionNumber || value.VersionNumber == latest.VersionNumber && value.ID < latest.ID {
				latest = &value
			}
		}
		if latest != nil && latest.MIMEType == "application/pdf" {
			source.Version = latest
			break
		}
	}
	var document any
	if source.Version != nil {
		v := source.Version
		document = []any{v.AttachmentID, v.ID, v.Checksum, v.SizeBytes}
	}
	key, _ := json.Marshal([]any{item.ID, item.MetadataRevision, !item.Deleted, document})
	source.Key = string(key)
	return source
}
func readLiteratureStudySource(ctx context.Context, query literatureItemQuery, owner, id string) (LiteratureStudySource, error) {
	item, err := readLiteratureItem(ctx, query, owner, id)
	if err != nil {
		return LiteratureStudySource{}, err
	}
	if item.Deleted {
		return LiteratureStudySource{}, ErrLiteratureUnavailable
	}
	return literatureStudySource(item), nil
}
func readLiteratureStudy(ctx context.Context, tx *sql.Tx, owner, id, language string) (LiteratureStudySource, LiteratureStudyView, error) {
	source, err := readLiteratureStudySource(ctx, tx, owner, id)
	view := LiteratureStudyView{Schema: LiteratureStudySchema, SourceKey: source.Key, Available: source.Abstract != "" || source.Version != nil && source.Version.Available}
	if err != nil {
		return source, view, err
	}
	var key, raw string
	err = tx.QueryRowContext(ctx, `SELECT revision,source_key,snapshot_json FROM literature_study_snapshots WHERE owner_user_id=? AND item_id=? AND language=?`, owner, id, language).Scan(&view.Revision, &key, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return source, view, nil
	}
	if err != nil {
		return source, view, err
	}
	if view.Revision < 1 || view.Revision > literatureStudyMaxRevision {
		return source, view, ErrLiteratureInvalid
	}
	if key == source.Key {
		snapshot, err := decodeStudySnapshot(raw)
		if err != nil || snapshot.SourceKey != key {
			return source, view, ErrLiteratureInvalid
		}
		view.Snapshot = &snapshot
	}
	return source, view, nil
}

// A single read transaction fixes metadata and immutable attachment selection.
func (s *Store) ReadLiteratureStudy(ctx context.Context, owner, id, language string) (LiteratureStudySource, LiteratureStudyView, error) {
	if !validLiteratureID(owner) || !validLiteratureID(id) || !ValidLiteratureStudyLanguage(language) {
		return LiteratureStudySource{}, LiteratureStudyView{}, ErrLiteratureInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LiteratureStudySource{}, LiteratureStudyView{}, err
	}
	defer tx.Rollback()
	source, view, err := readLiteratureStudy(ctx, tx, owner, id, language)
	if err == nil {
		err = tx.Commit()
	}
	return source, view, err
}
