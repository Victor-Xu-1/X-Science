package literaturediscovery

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type providerPaper struct {
	PaperID         string  `json:"paperId"`
	Title           string  `json:"title"`
	Abstract        *string `json:"abstract"`
	Venue           *string `json:"venue"`
	Year            *int    `json:"year"`
	PublicationDate *string `json:"publicationDate"`
	Authors         []struct {
		Name string `json:"name"`
	} `json:"authors"`
	ExternalIDs map[string]json.RawMessage `json:"externalIds"`
	Journal     *struct {
		Name   *string `json:"name"`
		Volume *string `json:"volume"`
		Pages  *string `json:"pages"`
	} `json:"journal"`
	PublicationTypes []string `json:"publicationTypes"`
}

func parseRecord(raw json.RawMessage) (Record, error) {
	var paper providerPaper
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &paper) != nil {
		return Record{}, sourceFailure("response-invalid")
	}
	if !paperIDValue.MatchString(paper.PaperID) || strings.TrimSpace(paper.Title) == "" || len(paper.Title) > 10000 ||
		!boundedText(paper.Abstract, 100000) || !boundedText(paper.Venue, 100000) ||
		!boundedText(paper.PublicationDate, 100000) || len(paper.Authors) > 2000 ||
		(paper.Year != nil && (*paper.Year < 0 || *paper.Year > 9999)) {
		return Record{}, sourceFailure("response-invalid")
	}
	entry := Record{
		PaperID: strings.ToLower(paper.PaperID), Title: strings.TrimSpace(paper.Title),
		Abstract: trimmedText(paper.Abstract), Authors: []string{}, Year: paper.Year,
		IssuedText: trimmedText(paper.PublicationDate), ContainerTitle: trimmedText(paper.Venue),
		ItemType: "document", Identifiers: []Identifier{},
	}
	entry.URL = "https://www.semanticscholar.org/paper/" + entry.PaperID
	if entry.IssuedText == "" && entry.Year != nil && *entry.Year != 0 {
		entry.IssuedText = strconv.Itoa(*entry.Year)
	}
	for _, author := range paper.Authors {
		name := strings.TrimSpace(author.Name)
		if name == "" || len(name) > 1000 {
			return Record{}, sourceFailure("response-invalid")
		}
		entry.Authors = append(entry.Authors, name)
	}
	if paper.Journal != nil {
		if !boundedText(paper.Journal.Name, 100000) || !boundedText(paper.Journal.Volume, 100000) || !boundedText(paper.Journal.Pages, 100000) {
			return Record{}, sourceFailure("response-invalid")
		}
		if paper.Journal.Name != nil {
			entry.ContainerTitle = trimmedText(paper.Journal.Name)
		}
		entry.Volume, entry.Pages = trimmedText(paper.Journal.Volume), trimmedText(paper.Journal.Pages)
	}
	if trimmedText(paper.Venue) != "" || (paper.Journal != nil && trimmedText(paper.Journal.Name) != "") {
		entry.ItemType = "journalArticle"
	}
	for _, kind := range paper.PublicationTypes {
		if kind == "Conference" {
			entry.ItemType = "conferencePaper"
		}
	}
	for _, kind := range paper.PublicationTypes {
		if kind == "Review" {
			entry.ItemType = "review"
		}
	}
	values := make(map[string]string)
	for key, rawValue := range paper.ExternalIDs {
		value, err := externalIDText(rawValue)
		if err != nil {
			return Record{}, err
		}
		values[key] = value
	}
	for _, pair := range [][2]string{{"DOI", "doi"}, {"PubMed", "pmid"}, {"PubMedCentral", "pmcid"}, {"ArXiv", "arxiv"}} {
		if identifier, valid := NormalizeIdentifier(Identifier{Scheme: pair[1], Value: values[pair[0]]}); valid {
			entry.Identifiers = append(entry.Identifiers, identifier)
		}
	}
	return entry, nil
}

func externalIDText(raw json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String(), nil
	}
	return "", sourceFailure("response-invalid")
}

func boundedText(value *string, maximum int) bool { return value == nil || len(*value) <= maximum }

func trimmedText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func cloneRecord(record Record) Record {
	record.Authors = append([]string{}, record.Authors...)
	record.Identifiers = append([]Identifier{}, record.Identifiers...)
	if record.Year != nil {
		year := *record.Year
		record.Year = &year
	}
	return record
}
