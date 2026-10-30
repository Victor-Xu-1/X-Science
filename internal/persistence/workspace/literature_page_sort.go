package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Page presentation is separate from membership filters and complete-ID/export order.
type LiteraturePageSort string

const (
	LiteraturePageTitleAscending  LiteraturePageSort = "title-asc"
	LiteraturePageTitleDescending LiteraturePageSort = "title-desc"
	literatureTitleCursorPrefix                      = "xscience-literature-title-v1."
)

var ErrLiteratureCursorStale = errors.New("literature page anchor changed")

type LiteraturePageReadOptions struct{ Sort LiteraturePageSort }

func (sort LiteraturePageSort) valid() bool {
	return sort == "" || sort == LiteraturePageTitleAscending || sort == LiteraturePageTitleDescending
}

func (sort LiteraturePageSort) orderBy() string {
	if sort == LiteraturePageTitleDescending {
		return `i.title DESC,i.id ASC`
	}
	return `i.title ASC,i.id ASC`
}

type literatureTitleAnchor struct{ id, titleHash string }
type literatureTitleCursorWire struct {
	Anchor    string             `json:"anchor"`
	TitleHash string             `json:"title_sha256"`
	ScopeHash string             `json:"scope_sha256"`
	Sort      LiteraturePageSort `json:"sort"`
}

func literatureTitleHash(title string) string {
	digest := sha256.Sum256([]byte(title))
	return hex.EncodeToString(digest[:])
}

// Length-prefixed raw strings bind every existing filter without JSON escaping
// expansion or lowering accepted input limits. Empty lifecycle means active.
func literatureTitleScopeHash(scope literatureQueryScope, sort LiteraturePageSort) string {
	lifecycle := scope.lifecycle
	if lifecycle == "" {
		lifecycle = LiteratureLifecycleActive
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("x-science.literature-title-scope.v1"))
	var length [8]byte
	for _, value := range []string{scope.owner, scope.query, scope.project, scope.collection, string(scope.readingStatus), string(lifecycle), string(sort)} {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func decodeLiteratureTitleCursor(cursor string, scope literatureQueryScope, sort LiteraturePageSort) (literatureTitleAnchor, error) {
	var empty literatureTitleAnchor
	if !strings.HasPrefix(cursor, literatureTitleCursorPrefix) {
		return empty, ErrLiteratureInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, literatureTitleCursorPrefix))
	if err != nil {
		return empty, ErrLiteratureInvalid
	}
	var wire literatureTitleCursorWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || wire.Sort != sort || wire.ScopeHash != literatureTitleScopeHash(scope, sort) {
		return empty, ErrLiteratureInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return empty, ErrLiteratureInvalid
	}
	id, err := base64.RawURLEncoding.DecodeString(wire.Anchor)
	titleHash, hashError := hex.DecodeString(wire.TitleHash)
	if err != nil || !validLiteratureID(string(id)) || hashError != nil || len(titleHash) != sha256.Size || wire.TitleHash != hex.EncodeToString(titleHash) {
		return empty, ErrLiteratureInvalid
	}
	return literatureTitleAnchor{id: string(id), titleHash: wire.TitleHash}, nil
}

func encodeLiteratureTitleCursor(anchor literatureTitleAnchor, scope literatureQueryScope, sort LiteraturePageSort) (string, error) {
	// Base64 the <=512-byte ID before JSON so control characters cannot expand
	// it sixfold. Full titles (up to 4096 bytes) never enter a cursor.
	raw, err := json.Marshal(literatureTitleCursorWire{
		Anchor: base64.RawURLEncoding.EncodeToString([]byte(anchor.id)), TitleHash: anchor.titleHash,
		ScopeHash: literatureTitleScopeHash(scope, sort), Sort: sort,
	})
	if err != nil {
		return "", err
	}
	encoded := literatureTitleCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > 4096 {
		return "", ErrLiteratureInvalid
	}
	return encoded, nil
}

// Resolve the title in the original authorized/filter snapshot, never from a
// client-supplied title. A missing/mutated anchor needs explicit first-page recovery.
func readLiteratureTitleAnchor(ctx context.Context, query literatureItemQuery, scope literatureQueryScope, anchor literatureTitleAnchor) (string, error) {
	from, args := scope.sql()
	var title string
	err := query.QueryRowContext(ctx, `SELECT i.title`+from+` AND i.id=?`, append(args, anchor.id)...).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLiteratureCursorStale
	}
	if err != nil {
		return "", err
	}
	if literatureTitleHash(title) != anchor.titleHash {
		return "", ErrLiteratureCursorStale
	}
	return title, nil
}
