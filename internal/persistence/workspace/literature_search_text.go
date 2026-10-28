package workspace

import (
	"database/sql/driver"
	"errors"
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// Register on the original driver before either Store entry point opens a
// connection. Its read pool and fresh-schema connections then inherit the same
// deterministic function. No normalized text or SQL function is stored in DB.
var registerLiteratureSearchText = sync.OnceValue(func() error {
	return sqlite.RegisterDeterministicScalarFunction("xscience_literature_search_text_v1", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if args[0] == nil {
				return "", nil
			}
			value, ok := args[0].(string)
			if !ok {
				return nil, errors.New("literature search text must be a string")
			}
			return normalizeLiteratureSearchText(value), nil
		})
})

// Match the reference's NFKC, ECMAScript whitespace collapse and default Unicode
// lowercase semantics. This is transient query text, never a metadata rewrite.
func normalizeLiteratureSearchText(value string) string {
	var compact strings.Builder
	space := false
	for _, character := range norm.NFKC.String(value) {
		if literatureSearchWhitespace(character) {
			space = compact.Len() > 0
			continue
		}
		if space {
			compact.WriteByte(' ')
			space = false
		}
		compact.WriteRune(character)
	}
	// Casers may retain context. A fresh one per call avoids sharing mutable
	// state between reader connections and preserves contextual final sigma.
	return cases.Lower(language.Und).String(compact.String())
}

func literatureSearchWhitespace(character rune) bool {
	switch character {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680',
		'\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	default:
		return character >= '\u2000' && character <= '\u200a'
	}
}
