package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

const literatureRatingCursorPrefix = "xscience-literature-rating-v1."

type literatureRatingAnchor struct {
	id, keyHash, titleHash string
	known                  bool
}

type literatureRatingCursorWire struct {
	Anchor    string             `json:"anchor"`
	KeyHash   string             `json:"rating_sha256"`
	TitleHash string             `json:"title_sha256"`
	Known     *bool              `json:"known"`
	ScopeHash string             `json:"scope_sha256"`
	Sort      LiteraturePageSort `json:"sort"`
}

func literatureRatingHash(key sql.NullInt64) string {
	if !key.Valid {
		return ""
	}
	return literatureTitleHash(strconv.FormatInt(key.Int64, 10))
}

func validLiteratureRatingHash(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && value == hex.EncodeToString(digest)
}

func decodeLiteratureRatingCursor(cursor string, scope literatureQueryScope, sort LiteraturePageSort) (literatureRatingAnchor, error) {
	var empty literatureRatingAnchor
	if sort != LiteraturePageRatingDescending || !strings.HasPrefix(cursor, literatureRatingCursorPrefix) {
		return empty, ErrLiteratureInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, literatureRatingCursorPrefix))
	if err != nil {
		return empty, ErrLiteratureInvalid
	}
	var wire literatureRatingCursorWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || wire.Known == nil || wire.Sort != sort || wire.ScopeHash != literatureTitleScopeHash(scope, sort) || decoder.Decode(new(any)) != io.EOF {
		return empty, ErrLiteratureInvalid
	}
	id, err := base64.RawURLEncoding.DecodeString(wire.Anchor)
	if err != nil || !validLiteratureID(string(id)) || !validLiteratureRatingHash(wire.TitleHash) {
		return empty, ErrLiteratureInvalid
	}
	if *wire.Known {
		if !validLiteratureRatingHash(wire.KeyHash) {
			return empty, ErrLiteratureInvalid
		}
	} else if wire.KeyHash != "" {
		return empty, ErrLiteratureInvalid
	}
	return literatureRatingAnchor{id: string(id), keyHash: wire.KeyHash, titleHash: wire.TitleHash, known: *wire.Known}, nil
}

func encodeLiteratureRatingCursor(anchor literatureRatingAnchor, scope literatureQueryScope, sort LiteraturePageSort) (string, error) {
	raw, err := json.Marshal(literatureRatingCursorWire{
		Anchor: base64.RawURLEncoding.EncodeToString([]byte(anchor.id)), KeyHash: anchor.keyHash, TitleHash: anchor.titleHash,
		Known: &anchor.known, ScopeHash: literatureTitleScopeHash(scope, sort), Sort: sort,
	})
	if err != nil {
		return "", err
	}
	encoded := literatureRatingCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > 4096 {
		return "", ErrLiteratureInvalid
	}
	return encoded, nil
}

// Resolve both effective sort keys inside the original authorized/filter read
// transaction. Unrelated metadata updates do not invalidate a continuation.
func readLiteratureRatingAnchor(ctx context.Context, query literatureItemQuery, scope literatureQueryScope, sort LiteraturePageSort, anchor literatureRatingAnchor) (sql.NullInt64, string, error) {
	from, args := scope.sql()
	var key sql.NullInt64
	var title string
	err := query.QueryRowContext(ctx, `SELECT `+sort.ratingExpression()+`,i.title`+from+` AND i.id=?`, append(args, anchor.id)...).Scan(&key, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return key, title, ErrLiteratureCursorStale
	}
	if err != nil {
		return key, title, err
	}
	if key.Valid != anchor.known || literatureRatingHash(key) != anchor.keyHash || literatureTitleHash(title) != anchor.titleHash {
		return key, title, ErrLiteratureCursorStale
	}
	return key, title, nil
}

func literatureRatingContinuation(expression string, key sql.NullInt64, title, id string) (string, []any) {
	tie := `(i.title>? OR (i.title=? AND i.id>?))`
	if !key.Valid {
		return ` AND (` + expression + ` IS NULL AND ` + tie + `)`, []any{title, title, id}
	}
	return ` AND (` + expression + `<? OR (` + expression + `=? AND ` + tie + `) OR ` + expression + ` IS NULL)`, []any{key.Int64, key.Int64, title, title, id}
}
