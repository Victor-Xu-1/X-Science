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

const literatureYearCursorPrefix = "xscience-literature-year-v1."

type literatureYearAnchor struct {
	id, keyHash, titleHash string
	known                  bool
}

type literatureYearCursorWire struct {
	Anchor    string             `json:"anchor"`
	KeyHash   string             `json:"year_sha256"`
	TitleHash string             `json:"title_sha256"`
	Known     *bool              `json:"known"`
	ScopeHash string             `json:"scope_sha256"`
	Sort      LiteraturePageSort `json:"sort"`
}

func literatureYearHash(key sql.NullInt64) string {
	if !key.Valid {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(key.Int64, 10)))
	return hex.EncodeToString(digest[:])
}

func validLiteratureYearHash(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && value == hex.EncodeToString(digest)
}

func decodeLiteratureYearCursor(cursor string, scope literatureQueryScope, sort LiteraturePageSort) (literatureYearAnchor, error) {
	var empty literatureYearAnchor
	if sort.yearExpression() == "" || !strings.HasPrefix(cursor, literatureYearCursorPrefix) {
		return empty, ErrLiteratureInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, literatureYearCursorPrefix))
	if err != nil {
		return empty, ErrLiteratureInvalid
	}
	var wire literatureYearCursorWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || wire.Known == nil || wire.Sort != sort || wire.ScopeHash != literatureTitleScopeHash(scope, sort) || decoder.Decode(new(any)) != io.EOF {
		return empty, ErrLiteratureInvalid
	}
	id, err := base64.RawURLEncoding.DecodeString(wire.Anchor)
	if err != nil || !validLiteratureID(string(id)) || !validLiteratureYearHash(wire.TitleHash) {
		return empty, ErrLiteratureInvalid
	}
	if *wire.Known {
		if !validLiteratureYearHash(wire.KeyHash) {
			return empty, ErrLiteratureInvalid
		}
	} else if wire.KeyHash != "" {
		return empty, ErrLiteratureInvalid
	}
	return literatureYearAnchor{id: string(id), keyHash: wire.KeyHash, titleHash: wire.TitleHash, known: *wire.Known}, nil
}

func encodeLiteratureYearCursor(anchor literatureYearAnchor, scope literatureQueryScope, sort LiteraturePageSort) (string, error) {
	raw, err := json.Marshal(literatureYearCursorWire{
		Anchor: base64.RawURLEncoding.EncodeToString([]byte(anchor.id)), KeyHash: anchor.keyHash, TitleHash: anchor.titleHash,
		Known: &anchor.known, ScopeHash: literatureTitleScopeHash(scope, sort), Sort: sort,
	})
	if err != nil {
		return "", err
	}
	encoded := literatureYearCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > 4096 {
		return "", ErrLiteratureInvalid
	}
	return encoded, nil
}

// Year ties include title. Revalidate both effective keys inside the original
// authorized/filter transaction, not the whole metadata or revision counter.
func readLiteratureYearAnchor(ctx context.Context, query literatureItemQuery, scope literatureQueryScope, sort LiteraturePageSort, anchor literatureYearAnchor) (sql.NullInt64, string, error) {
	from, args := scope.sql()
	var key sql.NullInt64
	var title string
	err := query.QueryRowContext(ctx, `SELECT `+sort.yearExpression()+`,i.title`+from+` AND i.id=?`, append(args, anchor.id)...).Scan(&key, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return key, title, ErrLiteratureCursorStale
	}
	if err != nil {
		return key, title, err
	}
	if key.Valid != anchor.known || literatureYearHash(key) != anchor.keyHash || literatureTitleHash(title) != anchor.titleHash {
		return key, title, ErrLiteratureCursorStale
	}
	return key, title, nil
}

func literatureYearContinuation(expression string, key sql.NullInt64, title, id string, ascending bool) (string, []any) {
	tie := `(i.title>? OR (i.title=? AND i.id>?))`
	if !key.Valid {
		return ` AND (` + expression + ` IS NULL AND ` + tie + `)`, []any{title, title, id}
	}
	comparison := "<"
	if ascending {
		comparison = ">"
	}
	return ` AND (` + expression + comparison + `? OR (` + expression + `=? AND ` + tie + `) OR ` + expression + ` IS NULL)`, []any{key.Int64, key.Int64, title, title, id}
}
