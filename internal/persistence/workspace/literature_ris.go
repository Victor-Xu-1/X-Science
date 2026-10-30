package workspace

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RIS values are literal UTF-8 text. No value may introduce a record/tag boundary.
func risText(value string) string {
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			out.WriteByte(' ')
		} else {
			out.WriteRune(r)
		}
	}
	parts := []string{}
	for _, part := range strings.Split(strings.TrimSpace(out.String()), " ") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " ")
}
func literatureRISEntry(item LiteratureItem) (string, error) {
	if strings.TrimSpace(item.Title) == "" || !utf8.ValidString(item.Title) {
		return "", ErrLiteratureInvalid
	}
	// PAT/STAND/GEN preserve known X-only item types; these are explicit X extensions.
	itemType := bibtexScalar(item.Metadata["item_type"])
	kind := map[string]string{"journalArticle": "JOUR", "review": "JOUR", "preprint": "JOUR", "conferencePaper": "CONF", "book": "BOOK", "bookSection": "CHAP", "thesis": "THES", "report": "RPRT", "dataset": "DATA", "standard": "STAND", "patent": "PAT", "webpage": "ELEC", "document": "GEN"}[itemType]
	if kind == "" {
		kind = "JOUR"
	}
	lines := []string{"TY  - " + kind}
	add := func(tag, value string) error {
		if !utf8.ValidString(value) {
			return ErrLiteratureInvalid
		}
		if text := risText(value); text != "" {
			lines = append(lines, tag+"  - "+text)
		}
		return nil
	}
	for _, author := range bibtexAuthors(item.Metadata["authors"]) {
		if err := add("AU", author); err != nil {
			return "", err
		}
	}
	if err := add("TI", item.Title); err != nil {
		return "", err
	}
	container := "T2"
	if itemType == "journalArticle" || itemType == "review" {
		container = "JO"
	}
	if err := add(container, bibtexScalar(item.Metadata["container_title"])); err != nil {
		return "", err
	}
	year := bibtexScalar(item.Metadata["year"])
	if parsed, err := strconv.ParseInt(year, 10, 64); err == nil && parsed > 0 {
		_ = add("PY", strconv.FormatInt(parsed, 10)+"///")
	}
	fields := [][2]string{{"volume", "VL"}, {"issue", "IS"}}
	for _, field := range fields {
		if err := add(field[1], bibtexScalar(item.Metadata[field[0]])); err != nil {
			return "", err
		}
	}
	pages := bibtexScalar(item.Metadata["pages"])
	if index := strings.IndexAny(pages, "-–"); index >= 0 {
		if err := add("SP", pages[:index]); err != nil {
			return "", err
		}
		if err := add("EP", pages[index+len(string([]rune(pages[index:])[0])):]); err != nil {
			return "", err
		}
	} else {
		if err := add("SP", pages); err != nil {
			return "", err
		}
	}
	fields = [][2]string{{"doi", "DO"}, {"url", "UR"}, {"issn", "SN"}, {"isbn", "SN"}, {"publisher", "PB"}, {"publisher_place", "CY"}, {"abstract", "AB"}, {"language", "LA"}}
	for _, field := range fields {
		if err := add(field[1], bibtexScalar(item.Metadata[field[0]])); err != nil {
			return "", err
		}
	}
	// Structured X tags retain each literal label; string keywords follow native comma separation.
	if keywords, ok := item.Metadata["keywords"].([]any); ok {
		for _, keyword := range keywords {
			if text, ok := keyword.(string); ok {
				if err := add("KW", text); err != nil {
					return "", err
				}
			}
		}
	} else {
		for _, keyword := range strings.Split(bibtexScalar(item.Metadata["keywords"]), ",") {
			if err := add("KW", keyword); err != nil {
				return "", err
			}
		}
	}
	for _, field := range [][2]string{{"pmid", "PMID"}, {"pmcid", "PMCID"}, {"arxiv", "arXiv"}} {
		if value := bibtexScalar(item.Metadata[field[0]]); value != "" {
			if err := add("N1", field[1]+": "+value); err != nil {
				return "", err
			}
		}
	}
	editor := "A2"
	if kind == "BOOK" {
		editor = "A3"
	}
	for _, role := range [][2]string{{"editors", editor}, {"translators", "A4"}} {
		for _, name := range bibtexAuthors(item.Metadata[role[0]]) {
			if err := add(role[1], name); err != nil {
				return "", err
			}
		}
	}
	for _, field := range [][2]string{{"edition", "ET"}, {"container_title_short", "J2"}} {
		if err := add(field[1], bibtexScalar(item.Metadata[field[0]])); err != nil {
			return "", err
		}
	}
	lines = append(lines, "ER  - ")
	return strings.Join(lines, "\n"), nil
}
