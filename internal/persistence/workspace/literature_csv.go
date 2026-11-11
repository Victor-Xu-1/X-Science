package workspace

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

var literatureCSVColumns = [...]string{"item_id", "title", "authors", "year", "item_type", "publication", "volume", "issue", "pages", "doi", "pmid", "pmcid", "arxiv_id", "url", "abstract", "language"}

// CSV contains only stored bibliographic facts. In particular, opaque metadata,
// personal notes, screening decisions and attachments are never serialized.
func literatureCSVEntry(item LiteratureItem) (string, error) {
	if strings.TrimSpace(item.Title) == "" {
		return "", ErrLiteratureInvalid
	}
	values := []string{item.ID, item.Title, strings.Join(bibtexAuthors(item.Metadata["authors"]), "; ")}
	for _, key := range []string{"year", "item_type", "container_title", "volume", "issue", "pages", "doi", "pmid", "pmcid", "arxiv", "url", "abstract", "language"} {
		value, err := literatureCSVScalar(item.Metadata[key])
		if err != nil {
			return "", err
		}
		values = append(values, value)
	}
	return literatureCSVRecord(values)
}

func literatureCSVScalar(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case json.Number:
		// The catalog's UseNumber preserves decimal/exponent and large-integer
		// lexemes. Never pass bibliographic numbers through a float conversion.
		raw, err := json.Marshal(value)
		if err != nil {
			return "", ErrLiteratureInvalid
		}
		return string(raw), nil
	default:
		return "", nil
	}
}

func literatureCSVRecord(values []string) (string, error) {
	quoted := make([]string, len(values))
	for index, value := range values {
		if !utf8.ValidString(value) {
			return "", ErrLiteratureInvalid
		}
		if literatureCSVFormulaPrefix(value) {
			value = "'" + value
		}
		value = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"), "\n", "\r\n")
		quoted[index] = `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return strings.Join(quoted, ","), nil
}

func literatureCSVFormulaPrefix(value string) bool {
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") {
		return true
	}
	for _, current := range value {
		// ECMAScript whitespace plus U+0000..001F, matching the CSV wire
		// convention. Unicode NEL and DEL are not extra whitespace guesses.
		if current <= 0x1f || current == ' ' || current == '\u00a0' || current == '\u1680' ||
			current >= '\u2000' && current <= '\u200a' || current == '\u2028' || current == '\u2029' ||
			current == '\u202f' || current == '\u205f' || current == '\u3000' || current == '\ufeff' {
			continue
		}
		return strings.ContainsRune("=+@-＝＋＠－", current)
	}
	return false
}
