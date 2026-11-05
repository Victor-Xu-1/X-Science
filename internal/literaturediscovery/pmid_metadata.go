package literaturediscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var lookupPMIDValue = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

type PMIDMetadataDraft struct {
	Schema    string         `json:"schema"`
	Reference string         `json:"reference"`
	PMID      string         `json:"pmid"`
	DOI       string         `json:"doi"`
	Title     string         `json:"title"`
	Metadata  map[string]any `json:"metadata"`
}

// LookupPMID reads an exact PubMed record from Europe PMC. The Library
// keeps its existing review and owner-scoped manual save.
func (lookup *DOIMetadataLookup) LookupPMID(caller context.Context, reference string) (PMIDMetadataDraft, error) {
	var empty PMIDMetadataDraft
	if len(reference) > 8192 || !utf8.ValidString(reference) || strings.IndexFunc(reference, unicode.IsControl) >= 0 {
		return empty, ErrDOIMetadataInvalid
	}
	identifier, valid := NormalizeIdentifier(Identifier{Scheme: "pmid", Value: reference})
	if !valid || !lookupPMIDValue.MatchString(identifier.Value) {
		return empty, ErrDOIMetadataInvalid
	}
	params := url.Values{
		"query":      {"EXT_ID:" + identifier.Value + " AND SRC:MED"},
		"resultType": {"core"}, "format": {"json"}, "pageSize": {"5"},
	}
	raw, err := lookup.fetchPublicMetadata(caller, "https://www.ebi.ac.uk/europepmc/webservices/rest/search?"+params.Encode())
	if err != nil {
		return empty, err
	}
	record, err := parseEuropePMCPMID(raw, identifier)
	if err != nil {
		return empty, err
	}
	return buildPMIDMetadataDraft(strings.TrimSpace(reference), identifier.Value, record)
}

type europePMCPMIDRecord struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	PMID     string `json:"pmid"`
	DOI      string `json:"doi"`
	Title    string `json:"title"`
	Abstract string `json:"abstractText"`
	Year     string `json:"pubYear"`
	Date     string `json:"firstPublicationDate"`
	Pages    string `json:"pageInfo"`
	Journal  struct {
		Volume  string `json:"volume"`
		Journal struct {
			Title string `json:"title"`
		} `json:"journal"`
	} `json:"journalInfo"`
	Authors struct {
		Author []struct {
			FirstName      string `json:"firstName"`
			LastName       string `json:"lastName"`
			FullName       string `json:"fullName"`
			CollectiveName string `json:"collectiveName"`
		} `json:"author"`
	} `json:"authorList"`
}

func parseEuropePMCPMID(raw []byte, id Identifier) (Record, error) {
	var payload struct {
		HitCount *int `json:"hitCount"`
		List     *struct {
			Results []europePMCPMIDRecord `json:"result"`
		} `json:"resultList"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.HitCount == nil || payload.List == nil ||
		payload.List.Results == nil || len(payload.List.Results) > 5 || *payload.HitCount != len(payload.List.Results) {
		return Record{}, ErrDOIMetadataUnavailable
	}
	if *payload.HitCount == 0 {
		return Record{}, ErrDOIMetadataNotFound
	}
	var match *europePMCPMIDRecord
	for i := range payload.List.Results {
		item := &payload.List.Results[i]
		if item.Source != "MED" || item.ID != id.Value {
			continue
		}
		if match != nil || (item.PMID != "" && item.PMID != id.Value) {
			return Record{}, ErrDOIMetadataUnavailable
		}
		match = item
	}
	if match == nil || len(match.Authors.Author) > 1000 {
		return Record{}, ErrDOIMetadataUnavailable
	}
	record := Record{
		Title: match.Title, Abstract: match.Abstract, ItemType: "journalArticle",
		ContainerTitle: match.Journal.Journal.Title, Volume: match.Journal.Volume, Pages: match.Pages,
		Identifiers: []Identifier{id},
	}
	if doi, ok := NormalizeIdentifier(Identifier{Scheme: "doi", Value: match.DOI}); ok {
		record.Identifiers = append(record.Identifiers, doi)
	}
	year := match.Year
	if year == "" && len(match.Date) >= 4 {
		year = match.Date[:4]
	}
	if value, err := strconv.Atoi(year); err == nil && value > 0 && value <= 9999 {
		record.Year = &value
	}
	for _, author := range match.Authors.Author {
		name := strings.TrimSpace(author.CollectiveName)
		if name == "" {
			name = strings.TrimSpace(author.FirstName + " " + author.LastName)
		}
		if name == "" {
			name = strings.TrimSpace(author.FullName)
		}
		if name != "" {
			record.Authors = append(record.Authors, name)
		}
	}
	return record, nil
}

func buildPMIDMetadataDraft(reference, pmid string, record Record) (PMIDMetadataDraft, error) {
	var empty PMIDMetadataDraft
	identifiers := make(map[string]string, len(record.Identifiers))
	for _, id := range record.Identifiers {
		identifiers[id.Scheme] = id.Value
	}
	title := metadataPlain(record.Title)
	if identifiers["pmid"] != pmid || title == "" || len(title) > 4096 || len(record.Authors) > 1000 {
		return empty, ErrDOIMetadataUnavailable
	}
	authors := make([]string, 0, len(record.Authors))
	for _, raw := range record.Authors {
		name := metadataPlain(raw)
		if name == "" || len(name) > 1000 {
			return empty, ErrDOIMetadataUnavailable
		}
		authors = append(authors, name)
	}
	metadata := map[string]any{
		"pmid": pmid, "authors": authors, "item_type": record.ItemType,
		"url": "https://pubmed.ncbi.nlm.nih.gov/" + pmid + "/",
	}
	doi := identifiers["doi"]
	if doi != "" {
		metadata["doi"] = doi
	}
	if record.Year != nil && *record.Year > 0 {
		metadata["year"] = fmt.Sprintf("%04d", *record.Year)
	}
	for key, raw := range map[string]string{
		"abstract": record.Abstract, "container_title": record.ContainerTitle,
		"volume": record.Volume, "pages": record.Pages,
	} {
		text := metadataPlain(raw)
		limit := 4096
		if key == "abstract" {
			limit = 64 << 10
		}
		if len(text) > limit {
			return empty, ErrDOIMetadataUnavailable
		}
		if text != "" {
			metadata[key] = text
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > 80<<10 {
		return empty, ErrDOIMetadataUnavailable
	}
	return PMIDMetadataDraft{
		Schema: "x-science.literature-pmid-draft.v1", Reference: reference,
		PMID: pmid, DOI: doi, Title: title, Metadata: metadata,
	}, nil
}
