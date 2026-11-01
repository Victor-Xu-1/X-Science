package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const AnnotationReadPageSize = 20

var (
	ErrAnnotationReadSource = errors.New("annotation source is unavailable in this ownership scope")
	ErrAnnotationReadCursor = errors.New("annotation cursor no longer identifies this source")
)

// ArtifactAnnotationSource is supplied by the admitted Reading owner, never by
// an annotation identifier or an arbitrary model-supplied project/path.
type ArtifactAnnotationSource struct {
	OwnerID, ProjectID, ArtifactID, VersionID, Checksum string
	SizeBytes                                           int64
}

type AnnotationPageCursor struct {
	ID         string    `json:"id"`
	LabelIndex int       `json:"label_index"`
	CreatedAt  time.Time `json:"created_at"`
}

type AnnotationReadPage struct {
	Items []Annotation
	Total int64
	Next  *AnnotationPageCursor
}

type annotationSourceReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateAnnotationReadSource(ctx context.Context, reader annotationSourceReader, source ArtifactAnnotationSource) error {
	for _, value := range []string{source.OwnerID, source.ProjectID, source.ArtifactID, source.VersionID, source.Checksum} {
		if value == "" || value != strings.TrimSpace(value) {
			return ErrAnnotationReadSource
		}
	}
	if source.SizeBytes <= 0 {
		return ErrAnnotationReadSource
	}
	var checksum string
	var size int64
	var available bool
	err := reader.QueryRowContext(ctx, `SELECT v.content_sha256,
		CASE WHEN COALESCE(v.storage_path, '') = '' THEN length(v.content) ELSE v.size_bytes END,
		v.content_available FROM artifact_versions v
		JOIN artifacts a ON a.id=v.artifact_id JOIN projects p ON p.id=a.project_id
		WHERE v.id=? AND a.id=? AND p.id=? AND p.user_id=?`,
		source.VersionID, source.ArtifactID, source.ProjectID, source.OwnerID).Scan(&checksum, &size, &available)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAnnotationReadSource
	}
	if err != nil {
		return err
	}
	if !available || checksum != source.Checksum || size != source.SizeBytes {
		return ErrAnnotationReadSource
	}
	return nil
}

func (s *Store) ValidateArtifactAnnotationSourceOwned(ctx context.Context, source ArtifactAnnotationSource) error {
	if s == nil || s.readDatabase() == nil || ctx == nil {
		return ErrAnnotationReadSource
	}
	return validateAnnotationReadSource(ctx, s.readDatabase(), source)
}

// ListArtifactAnnotationPageOwned reads the existing canonical table within one
// owner/resource snapshot. SQL limits content rows; it never loads a whole list
// and slices it afterward. Label order matches the existing annotation reader.
func (s *Store) ListArtifactAnnotationPageOwned(ctx context.Context, source ArtifactAnnotationSource, cursor *AnnotationPageCursor) (AnnotationReadPage, error) {
	var page AnnotationReadPage
	if s == nil || s.readDatabase() == nil || ctx == nil {
		return page, ErrAnnotationReadSource
	}
	tx, err := s.readDatabase().BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if err := validateAnnotationReadSource(ctx, tx, source); err != nil {
		return page, err
	}
	page, err = listAnnotationPageRows(ctx, tx, "project_id=? AND target_key=?", []any{source.ProjectID, "av:" + source.VersionID}, source.Checksum, cursor)
	if err != nil {
		return AnnotationReadPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return AnnotationReadPage{}, err
	}
	return page, nil
}

// Both admitted source kinds consume the same table, ordering and bounded page.
func listAnnotationPageRows(ctx context.Context, tx *sql.Tx, where string, scope []any, checksum string, cursor *AnnotationPageCursor) (AnnotationReadPage, error) {
	var page AnnotationReadPage
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM annotations WHERE `+where, scope...).Scan(&page.Total); err != nil {
		return AnnotationReadPage{}, err
	}
	after := -1
	if cursor != nil {
		var created time.Time
		if cursor.ID == "" || len(cursor.ID) > 1024 || cursor.LabelIndex < 0 || cursor.CreatedAt.IsZero() {
			return AnnotationReadPage{}, ErrAnnotationReadCursor
		}
		err := tx.QueryRowContext(ctx, `SELECT label_idx,created_at FROM annotations WHERE `+where+` AND id=?`, append(append([]any{}, scope...), cursor.ID)...).Scan(&after, &created)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (after != cursor.LabelIndex || !created.Equal(cursor.CreatedAt)) {
			return AnnotationReadPage{}, ErrAnnotationReadCursor
		}
		if err != nil {
			return AnnotationReadPage{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, annotationSelect+` WHERE `+where+` AND label_idx>?
		ORDER BY label_idx,created_at,id LIMIT ?`, append(append([]any{}, scope...), after, AnnotationReadPageSize+1)...)
	if err != nil {
		return AnnotationReadPage{}, err
	}
	page.Items = []Annotation{}
	for rows.Next() {
		annotation, err := scanAnnotation(rows)
		if err != nil {
			rows.Close()
			return AnnotationReadPage{}, err
		}
		if annotation.ContentChecksum != "" && annotation.ContentChecksum != checksum {
			rows.Close()
			return AnnotationReadPage{}, ErrAnnotationReadSource
		}
		page.Items = append(page.Items, annotation)
	}
	rowErr := rows.Err()
	closeErr := rows.Close()
	if rowErr != nil {
		return AnnotationReadPage{}, rowErr
	}
	if closeErr != nil {
		return AnnotationReadPage{}, closeErr
	}
	if len(page.Items) > AnnotationReadPageSize {
		page.Items = page.Items[:AnnotationReadPageSize]
		last := page.Items[len(page.Items)-1]
		page.Next = &AnnotationPageCursor{ID: last.ID, LabelIndex: last.LabelIndex, CreatedAt: last.CreatedAt}
	}
	return page, nil
}
