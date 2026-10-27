package workspace

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// Match ECMAScript \s after NFKC, rather than Go's broader Unicode IsSpace.
// In particular U+0085 is not ECMAScript whitespace, while U+FEFF is.
func literatureCollectionSpace(value rune) bool {
	switch value {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return value >= '\u2000' && value <= '\u200a'
}

func normalizeLiteratureCollectionText(value string, maximum int, required bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrLiteratureInvalid
	}
	var text strings.Builder
	units, pendingSpace := 0, false
	for _, char := range norm.NFKC.String(value) {
		if literatureCollectionSpace(char) {
			pendingSpace = text.Len() != 0
			continue
		}
		if pendingSpace {
			text.WriteByte(' ')
			units++
			pendingSpace = false
		}
		text.WriteRune(char)
		units++
		if char > 0xffff {
			units++
		}
		if units > maximum {
			return "", ErrLiteratureInvalid
		}
	}
	if required && units == 0 {
		return "", ErrLiteratureInvalid
	}
	return text.String(), nil
}

func literatureCollectionName(name, description string) (string, string, string, error) {
	normalized, err := normalizeLiteratureCollectionText(name, 200, true)
	if err != nil {
		return "", "", "", err
	}
	description, err = normalizeLiteratureCollectionText(description, 1000, false)
	if err != nil {
		return "", "", "", err
	}
	return normalized, cases.Lower(language.Und).String(normalized), description, nil
}

func literatureCollectionParent(id string) any {
	if id == "" {
		return nil
	}
	return id
}
