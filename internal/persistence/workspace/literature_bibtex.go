package workspace

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// ExportLiteratureBibtex reads the original catalogue in one snapshot. No mutation,
// attachment access, provider call or partially formatted success is performed.
func (s *Store) ExportLiteratureBibtex(ctx context.Context, owner string, ids []string) (LiteratureBibtex, error) {
	empty := LiteratureBibtex{}
	if !validLiteratureID(owner) || len(ids) == 0 || len(ids) > 200 {
		return empty, ErrLiteratureInvalid
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validLiteratureID(id) || !utf8.ValidString(id) || seen[id] {
			return empty, ErrLiteratureInvalid
		}
		seen[id] = true
	}
	database := s.readDatabase()
	if database == nil {
		return empty, errors.New("workspace store is closed")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		item, err := readLiteratureItem(ctx, tx, owner, id)
		if err != nil {
			return empty, err
		}
		if item.Deleted {
			return empty, ErrLiteratureUnavailable
		}
		entry, err := literatureBibtexEntry(item)
		if err != nil {
			return empty, err
		}
		entries = append(entries, entry)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return LiteratureBibtex{Schema: "x-science.literature-bibtex.v1", ItemIDs: append([]string(nil), ids...), Bibtex: strings.Join(entries, "\n\n") + "\n"}, nil
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
