package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const LiteratureAnnotationTargetKind = "literature-attachment-version"

type LiteratureAnnotationSource struct {
	OwnerID, ItemID, AttachmentID, VersionID, Checksum string
	SizeBytes                                          int64
	PageCount                                          int
	// Accepted is supplied only by a current admitted Reading authority. It
	// permits a retained tombstone for reads, never a public write.
	Accepted bool
}

func readLiteratureAnnotationSource(ctx context.Context, query annotationSourceReader, owner, version string, accepted bool) (LiteratureAnnotationSource, error) {
	source := LiteratureAnnotationSource{OwnerID: owner, VersionID: version, Accepted: accepted}
	if ctx == nil || owner == "" || version == "" || owner != strings.TrimSpace(owner) || version != strings.TrimSpace(version) {
		return source, ErrAnnotationReadSource
	}
	var deleted bool
	err := query.QueryRowContext(ctx, `SELECT i.id,a.id,v.checksum,v.size_bytes,v.page_count,i.deleted_at IS NOT NULL
 FROM literature_attachment_versions v JOIN literature_attachments a ON a.id=v.attachment_id JOIN literature_items i ON i.id=a.item_id
 WHERE v.id=? AND i.owner_user_id=? AND v.content_available=1 AND v.mime_type='application/pdf' AND v.page_count>0
 AND NOT EXISTS(SELECT 1 FROM blob_commit_markers b WHERE b.kind='literature-attachment-version' AND b.aggregate_id=v.id)`, version, owner).
		Scan(&source.ItemID, &source.AttachmentID, &source.Checksum, &source.SizeBytes, &source.PageCount, &deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && deleted && !accepted {
		return source, ErrAnnotationReadSource
	}
	return source, err
}

func (s *Store) LiteratureAnnotationSourceOwned(ctx context.Context, owner, version string, accepted bool) (LiteratureAnnotationSource, error) {
	if s == nil || s.readDatabase() == nil {
		return LiteratureAnnotationSource{}, ErrAnnotationReadSource
	}
	return readLiteratureAnnotationSource(ctx, s.readDatabase(), owner, version, accepted)
}

func validateLiteratureAnnotationSource(ctx context.Context, query annotationSourceReader, expected LiteratureAnnotationSource) error {
	actual, err := readLiteratureAnnotationSource(ctx, query, expected.OwnerID, expected.VersionID, expected.Accepted)
	if err != nil {
		return err
	}
	if actual != expected {
		return ErrAnnotationReadSource
	}
	return nil
}

func (s *Store) ValidateLiteratureAnnotationSourceOwned(ctx context.Context, source LiteratureAnnotationSource) error {
	if s == nil || s.readDatabase() == nil {
		return ErrAnnotationReadSource
	}
	return validateLiteratureAnnotationSource(ctx, s.readDatabase(), source)
}

func (s *Store) ListLiteratureAnnotationPageOwned(ctx context.Context, source LiteratureAnnotationSource, cursor *AnnotationPageCursor) (AnnotationReadPage, error) {
	if s == nil || s.readDatabase() == nil || ctx == nil {
		return AnnotationReadPage{}, ErrAnnotationReadSource
	}
	tx, err := s.readDatabase().BeginTx(ctx, nil)
	if err != nil {
		return AnnotationReadPage{}, err
	}
	defer tx.Rollback()
	if err = validateLiteratureAnnotationSource(ctx, tx, source); err != nil {
		return AnnotationReadPage{}, err
	}
	page, err := listAnnotationPageRows(ctx, tx, "library_owner_id=? AND target_key=?", []any{source.OwnerID, "litv:" + source.VersionID}, source.Checksum, cursor)
	if err != nil {
		return AnnotationReadPage{}, err
	}
	if err = tx.Commit(); err != nil {
		return AnnotationReadPage{}, err
	}
	return page, nil
}

// The scope predicate is derived from the stored target, never supplied by a
// caller. Old artifact/local rows keep their original project owner authority.
func requireAnnotationOwner(ctx context.Context, query annotationSourceReader, id, owner string) error {
	var project sql.NullString
	var kind, key string
	var libraryOwner sql.NullString
	err := query.QueryRowContext(ctx, `SELECT project_id,target_kind,target_key,library_owner_id FROM annotations WHERE id=?`, id).Scan(&project, &kind, &key, &libraryOwner)
	if err != nil {
		return err
	}
	if kind == LiteratureAnnotationTargetKind {
		if project.Valid || !libraryOwner.Valid || libraryOwner.String != owner || !strings.HasPrefix(key, "litv:litv_") {
			return ErrAnnotationReadSource
		}
		_, err = readLiteratureAnnotationSource(ctx, query, owner, strings.TrimPrefix(key, "litv:"), false)
		return err
	}
	var actual string
	if err = query.QueryRowContext(ctx, `SELECT user_id FROM projects WHERE id=?`, project.String).Scan(&actual); err != nil {
		return err
	}
	if !project.Valid || actual != owner {
		return ErrAnnotationReadSource
	}
	return nil
}

func (s *Store) AnnotationOwnedByContext(ctx context.Context, id, owner string) (bool, error) {
	if s == nil || s.readDatabase() == nil {
		return false, ErrAnnotationReadSource
	}
	err := requireAnnotationOwner(ctx, s.readDatabase(), id, owner)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrAnnotationReadSource) {
		return false, nil
	}
	return err == nil, err
}

func annotationCreateScope(ctx context.Context, tx *sql.Tx, input CreateAnnotationInput, owner string) (string, []any, error) {
	if input.TargetKind == LiteratureAnnotationTargetKind {
		if input.ProjectID != "" || !strings.HasPrefix(input.TargetKey, "litv:litv_") {
			return "", nil, ErrAnnotationReadSource
		}
		source, err := readLiteratureAnnotationSource(ctx, tx, owner, strings.TrimPrefix(input.TargetKey, "litv:"), false)
		if err != nil {
			return "", nil, err
		}
		if source.Checksum != input.ContentChecksum {
			return "", nil, ErrAnnotationReadSource
		}
		return "library_owner_id=? AND target_key=?", []any{owner, input.TargetKey}, nil
	}
	if err := requireProjectOwnerTx(ctx, tx, input.ProjectID, owner); err != nil {
		return "", nil, err
	}
	return "project_id=? AND target_key=?", []any{input.ProjectID, input.TargetKey}, nil
}

func annotationLibraryOwner(kind, owner string) any {
	if kind == LiteratureAnnotationTargetKind {
		return owner
	}
	return nil
}

// Exactly one canonical source is present. Both branches use the same bounded
// annotations-table reader; this type conveys no authority by itself.
type AnnotationReadSource struct {
	Artifact   *ArtifactAnnotationSource
	Literature *LiteratureAnnotationSource
}

func (s *Store) ListAnnotationPageOwned(ctx context.Context, source AnnotationReadSource, cursor *AnnotationPageCursor) (AnnotationReadPage, error) {
	if source.Artifact != nil && source.Literature == nil {
		return s.ListArtifactAnnotationPageOwned(ctx, *source.Artifact, cursor)
	}
	if source.Literature != nil && source.Artifact == nil {
		return s.ListLiteratureAnnotationPageOwned(ctx, *source.Literature, cursor)
	}
	return AnnotationReadPage{}, ErrAnnotationReadSource
}
func (s *Store) ValidateAnnotationSourceOwned(ctx context.Context, source AnnotationReadSource) error {
	if source.Artifact != nil && source.Literature == nil {
		return s.ValidateArtifactAnnotationSourceOwned(ctx, *source.Artifact)
	}
	if source.Literature != nil && source.Artifact == nil {
		return s.ValidateLiteratureAnnotationSourceOwned(ctx, *source.Literature)
	}
	return ErrAnnotationReadSource
}
