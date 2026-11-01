package literaturediscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrDOIMetadataInvalid     = errors.New("invalid DOI metadata request")
	ErrDOIMetadataUnavailable = errors.New("DOI metadata unavailable")
	ErrDOIMetadataNotFound    = errors.New("DOI metadata not found")
	ErrDOIMetadataRateLimited = errors.New("DOI metadata rate limited")
	ErrDOIMetadataTimeout     = errors.New("DOI metadata timed out")
	metadataMarkup            = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
)

type DOIMetadataDraft struct {
	Schema    string         `json:"schema"`
	Reference string         `json:"reference"`
	DOI       string         `json:"doi"`
	Title     string         `json:"title"`
	Metadata  map[string]any `json:"metadata"`
}

// DOIMetadataLookup only reads a proposal. Catalogue writes, ownership,
// idempotency and duplicate handling remain with the existing manual command.
type DOIMetadataLookup struct {
	clientFor func(context.Context, string) (*http.Client, error)
}

func NewDOIMetadataLookup(clientFor func(context.Context, string) (*http.Client, error)) *DOIMetadataLookup {
	return &DOIMetadataLookup{clientFor: clientFor}
}

func metadataInputDOI(input string) (string, bool) {
	if !utf8.ValidString(input) || len(input) > 8192 {
		return "", false
	}
	value := strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(value), "http:") || strings.HasPrefix(strings.ToLower(value), "https:") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.User != nil || parsed.Port() != "" || parsed.Opaque != "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			(!strings.EqualFold(parsed.Hostname(), "doi.org") && !strings.EqualFold(parsed.Hostname(), "dx.doi.org")) {
			return "", false
		}
		value = strings.TrimPrefix(parsed.Path, "/")
	}
	return NormalizeDOI(value)
}

func (lookup *DOIMetadataLookup) Lookup(caller context.Context, reference string) (DOIMetadataDraft, error) {
	var empty DOIMetadataDraft
	doi, valid := metadataInputDOI(reference)
	if !valid {
		return empty, ErrDOIMetadataInvalid
	}
	if err := caller.Err(); err != nil {
		return empty, err
	}
	if lookup == nil || lookup.clientFor == nil {
		return empty, ErrDOIMetadataUnavailable
	}
	ctx, cancel := context.WithTimeout(caller, 15*time.Second)
	defer cancel()
	rawURL := "https://api.crossref.org/works/" + url.PathEscape(doi)
	base, err := lookup.clientFor(ctx, rawURL)
	if err != nil || base == nil {
		return empty, metadataLookupError(caller, ctx)
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return empty, ErrDOIMetadataInvalid
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return empty, metadataLookupError(caller, ctx)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return empty, ErrDOIMetadataNotFound
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return empty, ErrDOIMetadataRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return empty, ErrDOIMetadataUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if caller.Err() != nil || ctx.Err() != nil {
		return empty, metadataLookupError(caller, ctx)
	}
	if err != nil || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return empty, ErrDOIMetadataUnavailable
	}
	draft, err := parseDOIMetadata(raw, doi)
	if err != nil {
		return empty, err
	}
	draft.Reference = strings.TrimSpace(reference)
	return draft, nil
}

func metadataLookupError(caller, request context.Context) error {
	if caller.Err() != nil {
		return caller.Err()
	}
	if errors.Is(request.Err(), context.DeadlineExceeded) {
		return ErrDOIMetadataTimeout
	}
	return ErrDOIMetadataUnavailable
}

type metadataDate struct {
	Parts [][]int `json:"date-parts"`
}

func metadataPlain(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(metadataMarkup.ReplaceAllString(value, ""))), " ")
}

func parseDOIMetadata(raw []byte, doi string) (DOIMetadataDraft, error) {
	var empty DOIMetadataDraft
	var envelope struct {
		Status  string `json:"status"`
		Kind    string `json:"message-type"`
		Message struct {
			DOI       string   `json:"DOI"`
			Title     []string `json:"title"`
			Type      string   `json:"type"`
			Container []string `json:"container-title"`
			Abstract  string   `json:"abstract"`
			Publisher string   `json:"publisher"`
			Volume    string   `json:"volume"`
			Issue     string   `json:"issue"`
			Pages     string   `json:"page"`
			Authors   []struct {
				Given  string `json:"given"`
				Family string `json:"family"`
				Name   string `json:"name"`
			} `json:"author"`
			Published metadataDate `json:"published"`
			Issued    metadataDate `json:"issued"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Status != "ok" || envelope.Kind != "work" ||
		len(envelope.Message.Title) == 0 || len(envelope.Message.Authors) > 1000 {
		return empty, ErrDOIMetadataUnavailable
	}
	actual, valid := NormalizeDOI(envelope.Message.DOI)
	if !valid || actual != doi {
		return empty, ErrDOIMetadataUnavailable
	}
	title := metadataPlain(envelope.Message.Title[0])
	if title == "" || len(title) > 4096 {
		return empty, ErrDOIMetadataUnavailable
	}
	authors := []string{}
	for _, author := range envelope.Message.Authors {
		name := metadataPlain(strings.TrimSpace(author.Given + " " + author.Family))
		if name == "" {
			name = metadataPlain(author.Name)
		}
		if name == "" || len(name) > 1000 {
			return empty, ErrDOIMetadataUnavailable
		}
		authors = append(authors, name)
	}
	kind := map[string]string{
		"journal-article": "journalArticle", "posted-content": "preprint",
		"book": "book", "edited-book": "book", "monograph": "book", "reference-book": "book",
		"book-chapter": "bookSection", "book-part": "bookSection", "book-section": "bookSection", "reference-entry": "bookSection",
		"proceedings-article": "conferencePaper", "dataset": "dataset", "dissertation": "thesis",
		"report": "report", "report-series": "report", "standard": "standard", "standard-series": "standard",
	}[envelope.Message.Type]
	if kind == "" {
		kind = "document"
	}
	canonicalURL := (&url.URL{Scheme: "https", Host: "doi.org", Path: "/" + doi}).String()
	metadata := map[string]any{"doi": doi, "authors": authors, "item_type": kind, "url": canonicalURL}
	fields := map[string]string{"abstract": envelope.Message.Abstract, "publisher": envelope.Message.Publisher,
		"volume": envelope.Message.Volume, "issue": envelope.Message.Issue, "pages": envelope.Message.Pages}
	if len(envelope.Message.Container) > 0 {
		fields["container_title"] = envelope.Message.Container[0]
	}
	for key, raw := range fields {
		text := metadataPlain(raw)
		maximum := 4096
		if key == "abstract" {
			maximum = 64 << 10
		}
		if len(text) > maximum {
			return empty, ErrDOIMetadataUnavailable
		}
		if text != "" {
			metadata[key] = text
		}
	}
	for _, date := range []metadataDate{envelope.Message.Published, envelope.Message.Issued} {
		if len(date.Parts) > 0 && len(date.Parts[0]) > 0 && date.Parts[0][0] > 0 && date.Parts[0][0] <= 9999 {
			metadata["year"] = fmt.Sprintf("%04d", date.Parts[0][0])
			break
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > 80<<10 {
		return empty, ErrDOIMetadataUnavailable
	}
	return DOIMetadataDraft{Schema: "x-science.literature-metadata-draft.v1", DOI: doi, Title: title, Metadata: metadata}, nil
}
