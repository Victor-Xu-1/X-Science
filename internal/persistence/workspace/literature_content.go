package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// accepted permits only retained tombstones, not another owner. The server must
// establish an accepted snapshot before using that mode; public content is live.
func (s *Store) OpenLiteratureVersionContent(ctx context.Context, owner, id string, accepted bool) (string, LiteratureVersion, ArtifactContentReader, error) {
	var v LiteratureVersion
	var item string
	var deleted sql.NullString
	err := s.readDatabase().QueryRowContext(ctx, `SELECT i.id,i.deleted_at,v.id,v.attachment_id,v.version_number,v.filename,v.mime_type,v.size_bytes,v.checksum,v.page_count,v.storage_path,v.content_available
	 FROM literature_attachment_versions v JOIN literature_attachments a ON a.id=v.attachment_id JOIN literature_items i ON i.id=a.item_id
	 WHERE v.id=? AND i.owner_user_id=? AND NOT EXISTS(SELECT 1 FROM blob_commit_markers b WHERE b.kind='literature-attachment-version' AND b.aggregate_id=v.id)`, id, owner).Scan(&item, &deleted, &v.ID, &v.AttachmentID, &v.VersionNumber, &v.Filename, &v.MIMEType, &v.SizeBytes, &v.Checksum, &v.PageCount, &v.StoragePath, &v.Available)
	if errors.Is(err, sql.ErrNoRows) {
		return item, v, nil, ErrLiteratureUnavailable
	}
	if err != nil {
		return item, v, nil, err
	}
	if !v.Available || deleted.Valid && !accepted {
		return item, v, nil, ErrLiteratureUnavailable
	}
	if !strings.HasPrefix(v.StoragePath, "literature-versions/") {
		return item, v, nil, ErrLiteratureUnavailable
	}
	path, err := s.blobAbsolute(v.StoragePath)
	if err != nil {
		return item, v, nil, err
	}
	file, err := openRegularFile(path)
	if err != nil {
		return item, v, nil, ErrLiteratureUnavailable
	}
	hash := sha256.New()
	size, err := io.Copy(hash, &contextReader{ctx: ctx, reader: io.LimitReader(file, v.SizeBytes+1)})
	if err != nil || size != v.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != v.Checksum {
		_ = file.Close()
		return item, v, nil, ErrLiteratureUnavailable
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return item, v, nil, err
	}
	return item, v, file, nil
}

// Called after canonical marker recovery. All immutable versions, including
// tombstones, retain their bytes. There is intentionally no hard-purge policy.
func (s *Store) reconcileLiteratureBlobs(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,storage_path FROM literature_attachment_versions WHERE content_available=1`)
	if err != nil {
		return err
	}
	type entry struct{ id, path string }
	entries := []entry{}
	for rows.Next() {
		var value entry
		if err = rows.Scan(&value.id, &value.path); err != nil {
			break
		}
		entries = append(entries, value)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, value := range entries {
		if !strings.HasPrefix(value.path, "literature-versions/") {
			return ErrLiteratureUnavailable
		}
		path, err := s.blobAbsolute(value.path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if _, err = s.db.ExecContext(ctx, `UPDATE literature_attachment_versions SET content_available=0 WHERE id=?`, value.id); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ErrLiteratureUnavailable
		}
	}
	return ensurePrivateDirectory(filepath.Join(s.blobRoot, "literature-versions"))
}
