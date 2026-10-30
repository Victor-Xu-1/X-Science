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
	"strings"
)

const literatureDateCursorPrefix = "xscience-literature-date-v1."

type literatureDateAnchor struct {
	id, keyHash string
	known       bool
}

type literatureDateCursorWire struct {
	Anchor    string             `json:"anchor"`
	KeyHash   string             `json:"date_sha256"`
	Known     *bool              `json:"known"`
	ScopeHash string             `json:"scope_sha256"`
	Sort      LiteraturePageSort `json:"sort"`
}

func literatureDateHash(key sql.NullString) string {
	if !key.Valid {
		return ""
	}
	digest := sha256.Sum256([]byte(key.String))
	return hex.EncodeToString(digest[:])
}

func decodeLiteratureDateCursor(cursor string, scope literatureQueryScope, sort LiteraturePageSort) (literatureDateAnchor, error) {
	var empty literatureDateAnchor
	if sort.dateExpression() == "" || !strings.HasPrefix(cursor, literatureDateCursorPrefix) {
		return empty, ErrLiteratureInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, literatureDateCursorPrefix))
	if err != nil {
		return empty, ErrLiteratureInvalid
	}
	var wire literatureDateCursorWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	// Reuse the existing length-framed full-scope identity without changing
	// Title/legacy bytes. The distinct prefix and explicit sort bind the family.
	if decoder.Decode(&wire) != nil || wire.Known == nil || wire.Sort != sort || wire.ScopeHash != literatureTitleScopeHash(scope, sort) || decoder.Decode(new(any)) != io.EOF {
		return empty, ErrLiteratureInvalid
	}
	id, err := base64.RawURLEncoding.DecodeString(wire.Anchor)
	if err != nil || !validLiteratureID(string(id)) {
		return empty, ErrLiteratureInvalid
	}
	if *wire.Known {
		digest, err := hex.DecodeString(wire.KeyHash)
		if err != nil || len(digest) != sha256.Size || wire.KeyHash != hex.EncodeToString(digest) {
			return empty, ErrLiteratureInvalid
		}
	} else if wire.KeyHash != "" {
		return empty, ErrLiteratureInvalid
	}
	return literatureDateAnchor{id: string(id), keyHash: wire.KeyHash, known: *wire.Known}, nil
}

func encodeLiteratureDateCursor(anchor literatureDateAnchor, scope literatureQueryScope, sort LiteraturePageSort) (string, error) {
	raw, err := json.Marshal(literatureDateCursorWire{
		Anchor: base64.RawURLEncoding.EncodeToString([]byte(anchor.id)), KeyHash: anchor.keyHash, Known: &anchor.known,
		ScopeHash: literatureTitleScopeHash(scope, sort), Sort: sort,
	})
	if err != nil {
		return "", err
	}
	encoded := literatureDateCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > 4096 {
		return "", ErrLiteratureInvalid
	}
	return encoded, nil
}

// Validate only the selected date, in the same authorized/filter transaction.
// Equivalent instants do not go stale, nor do updates to a different date.
func readLiteratureDateAnchor(ctx context.Context, query literatureItemQuery, scope literatureQueryScope, sort LiteraturePageSort, anchor literatureDateAnchor) (sql.NullString, error) {
	from, args := scope.sql()
	var key sql.NullString
	err := query.QueryRowContext(ctx, `SELECT `+sort.dateExpression()+from+` AND i.id=?`, append(args, anchor.id)...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return key, ErrLiteratureCursorStale
	}
	if err != nil {
		return key, err
	}
	if key.Valid != anchor.known || literatureDateHash(key) != anchor.keyHash {
		return key, ErrLiteratureCursorStale
	}
	return key, nil
}
