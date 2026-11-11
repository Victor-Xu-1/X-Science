package workspace

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLiteratureCSVStoredFactsAndQuotedCells(t *testing.T) {
	item := LiteratureItem{ID: "literal-id", Title: "研究, \"quoted\"\nsecond\rthird\r\nlast", Metadata: map[string]any{
		"authors": []any{"Ada, A", "研究组", map[string]any{"given": "not inferred"}}, "year": json.Number("2024"),
		"item_type": "journalArticle", "container_title": " Journal ", "volume": json.Number("900719925474099312345"), "issue": json.Number("1.25e+3"),
		"pages": "10–20", "doi": "10.1234/example", "pmid": "123", "pmcid": "PMC456", "arxiv": "2401.12345", "url": "https://example.test/a,b", "abstract": "\tplain\n\"quoted\"", "language": "zh",
		"personal_note": "private-note", "screening": "private-screening", "attachments": "private-path", "notes": "private-other", "editors": []string{"not an author"},
	}, Attachments: []LiteratureAttachment{{ID: "private-attachment"}}}
	before, _ := json.Marshal(item)
	entry, err := literatureReferenceEntry(item, LiteratureExportFormat("csv"))
	if err != nil {
		t.Fatal(err)
	}
	row, err := csv.NewReader(strings.NewReader(entry + "\r\n")).Read()
	want := []string{"literal-id", "研究, \"quoted\"\nsecond\nthird\nlast", "Ada, A; 研究组", "2024", "journalArticle", " Journal ", "900719925474099312345", "1.25e+3", "10–20", "10.1234/example", "123", "PMC456", "2401.12345", "https://example.test/a,b", "'\tplain\n\"quoted\"", "zh"}
	if err != nil || !reflect.DeepEqual(row, want) {
		t.Fatalf("CSV facts=%q want=%q err=%v", row, want, err)
	}
	if !strings.Contains(entry, `"研究, ""quoted""`+"\r\nsecond\r\nthird\r\nlast\"") || strings.Contains(strings.ReplaceAll(entry, "\r\n", ""), "\n") {
		t.Fatalf("CSV quote/newline framing changed: %q", entry)
	}
	for _, forbidden := range []string{"private-note", "private-screening", "private-path", "private-other", "private-attachment", "not inferred", "not an author"} {
		if strings.Contains(entry, forbidden) {
			t.Errorf("CSV leaked %q", forbidden)
		}
	}
	after, _ := json.Marshal(item)
	if string(before) != string(after) {
		t.Fatal("CSV export mutated the catalog item")
	}
}

func TestLiteratureCSVSpreadsheetPrefixesAndMissingFacts(t *testing.T) {
	for _, test := range []struct {
		value  string
		shield bool
	}{{"=SUM(1,2)", true}, {"+cmd", true}, {"@cmd", true}, {"-cmd", true}, {"＝cmd", true}, {"＋cmd", true}, {"＠cmd", true}, {"－cmd", true}, {" \u0000\u001f\u00a0\u3000=cmd", true}, {"\ufeff=cmd", true}, {"\tplain", true}, {"\rplain", true}, {"\nplain", true}, {"ordinary=title", false}, {"\u0085=not-whitespace", false}, {"\u007f=not-whitespace", false}} {
		t.Run(test.value, func(t *testing.T) {
			entry, err := literatureReferenceEntry(LiteratureItem{ID: "one", Title: test.value, Metadata: map[string]any{"issuedText": "2025-01-01", "date": "2024", "year": map[string]any{"value": 2020}}}, LiteratureExportFormat("csv"))
			if err != nil {
				t.Fatal(err)
			}
			row, err := csv.NewReader(strings.NewReader(entry + "\r\n")).Read()
			if err != nil || len(row) != 16 || strings.HasPrefix(row[1], "'") != test.shield {
				t.Fatalf("CSV spreadsheet prefix row=%q err=%v", row, err)
			}
			for _, missing := range row[2:] {
				if missing != "" {
					t.Fatalf("CSV inferred a missing bibliographic fact: %q", row)
				}
			}
		})
	}
}

func TestLiteratureCSVRejectsInvalidTextOrNumericLexeme(t *testing.T) {
	for _, item := range []LiteratureItem{{ID: "one", Title: string([]byte{0xff})}, {ID: "one", Title: "Title", Metadata: map[string]any{"abstract": string([]byte{0xff})}}, {ID: "one", Title: "Title", Metadata: map[string]any{"volume": json.Number("not-a-number")}}} {
		if _, err := literatureReferenceEntry(item, LiteratureExportFormat("csv")); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("invalid metadata accepted: %v", err)
		}
	}
}
