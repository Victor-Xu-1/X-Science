package workspace

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// A dot cannot occur in the old raw-URL-base64 format: an old reader must
// reject deleted cursors before its permissive JSON decoder sees any fields.
const literatureDeletedCursorPrefix = "xscience-literature-deleted-v1."

func decodeLiteratureCursor(cursor string, expected literatureCursor, lifecycle LiteratureLifecycle) (literatureCursor, error) {
	if lifecycle == LiteratureLifecycleDeleted {
		if !strings.HasPrefix(cursor, literatureDeletedCursorPrefix) {
			return expected, ErrLiteratureInvalid
		}
		cursor = strings.TrimPrefix(cursor, literatureDeletedCursorPrefix)
	} else if strings.HasPrefix(cursor, literatureDeletedCursorPrefix) {
		return expected, ErrLiteratureInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	var decoded literatureCursor
	if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded.Query != expected.Query || decoded.Project != expected.Project || decoded.Owner != expected.Owner || decoded.Collection != expected.Collection || decoded.ReadingStatus != expected.ReadingStatus ||
		expected.Collection == "" && decoded.Order != nil || expected.Collection != "" && (decoded.Order == nil || *decoded.Order < 0) {
		return expected, ErrLiteratureInvalid
	}
	return decoded, nil
}

func encodeLiteratureCursor(cursor literatureCursor, lifecycle LiteratureLifecycle) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if lifecycle == LiteratureLifecycleDeleted {
		encoded = literatureDeletedCursorPrefix + encoded
	}
	if len(encoded) > 4096 {
		return "", ErrLiteratureInvalid
	}
	return encoded, nil
}
