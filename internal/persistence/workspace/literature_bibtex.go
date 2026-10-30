package workspace

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type LiteratureBibtex struct {
	Schema  string   `json:"schema"`
	ItemIDs []string `json:"item_ids"`
	Bibtex  string   `json:"bibtex"`
}

// Cached clients keep their exact wire shape while sharing the canonical reader.
func (s *Store) ExportLiteratureBibtex(ctx context.Context, owner string, ids []string) (LiteratureBibtex, error) {
	result, err := s.ExportLiteratureReferences(ctx, owner, ids, LiteratureExportBibtex)
	if err != nil {
		return LiteratureBibtex{}, err
	}
	return LiteratureBibtex{Schema: "x-science.literature-bibtex.v1", ItemIDs: result.ItemIDs, Bibtex: result.Content}, nil
}

func literatureBibtexEntry(item LiteratureItem) (string, error) {
	if strings.TrimSpace(item.Title) == "" || !utf8.ValidString(item.Title) {
		return "", ErrLiteratureInvalid
	}
	kind := map[string]string{"journalArticle": "article", "review": "article", "conferencePaper": "inproceedings", "book": "book", "bookSection": "incollection", "report": "techreport"}[bibtexScalar(item.Metadata["item_type"])]
	if kind == "" {
		kind = "misc"
	}
	// Hex encodes the complete ID, rather than truncating/hash-colliding author/year keys.
	fields := []string{"title = {" + escapeBibtex(item.Title) + "}"}
	if names := bibtexAuthors(item.Metadata["authors"]); len(names) > 0 {
		// Stored free-text names have no family/given split. Preserve each literal name.
		for i := range names {
			if !utf8.ValidString(names[i]) {
				return "", ErrLiteratureInvalid
			}
			names[i] = "{" + escapeBibtex(names[i]) + "}"
		}
		fields = append(fields, "author = {"+strings.Join(names, " and ")+"}")
	}
	keys := [][2]string{{"year", "year"}, {"doi", "doi"}, {"url", "url"}, {"volume", "volume"}, {"issue", "number"}, {"pages", "pages"}, {"publisher", "publisher"}, {"publisher_place", "address"}, {"edition", "edition"}}
	container := "howpublished"
	if kind == "article" {
		container = "journal"
	} else if kind == "inproceedings" || kind == "incollection" {
		container = "booktitle"
	}
	keys = append(keys, [2]string{"container_title", container})
	for _, key := range keys {
		if value := bibtexScalar(item.Metadata[key[0]]); value != "" {
			if !utf8.ValidString(value) {
				return "", ErrLiteratureInvalid
			}
			fields = append(fields, key[1]+" = {"+escapeBibtex(value)+"}")
		}
	}
	return fmt.Sprintf("@%s{x_%s,\n  %s\n}", kind, hex.EncodeToString([]byte(item.ID)), strings.Join(fields, ",\n  ")), nil
}

func bibtexScalar(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		if _, err := v.Int64(); err == nil {
			return v.String()
		}
	}
	return ""
}
func bibtexAuthors(value any) []string {
	names := []string{}
	switch v := value.(type) {
	case string:
		names = strings.Split(v, ";")
	case []any:
		for _, name := range v {
			if text, ok := name.(string); ok {
				names = append(names, text)
			}
		}
	case []string:
		names = append(names, v...)
	}
	result := []string{}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			result = append(result, name)
		}
	}
	return result
}
func escapeBibtex(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '\\':
			out.WriteString(`\textbackslash{}`)
		case '{':
			out.WriteString(`\textbraceleft{}`)
		case '}':
			out.WriteString(`\textbraceright{}`)
		case '$', '%', '#', '&', '_':
			out.WriteRune('\\')
			out.WriteRune(r)
		case '^':
			out.WriteString(`\textasciicircum{}`)
		case '~':
			out.WriteString(`\textasciitilde{}`)
		default:
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				out.WriteByte(' ')
			} else {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}
