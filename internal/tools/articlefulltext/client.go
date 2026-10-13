package articlefulltext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"x-science/internal/buildinfo"
	"x-science/internal/mcpdirectory"
)

const (
	defaultBaseURL  = "https://www.ebi.ac.uk/europepmc/webservices/rest"
	defaultMaxBytes = int64(8 * 1024 * 1024)
	searchMaxBytes  = int64(1024 * 1024)
)

var (
	doiPattern   = regexp.MustCompile(`(?i)^10\.[0-9]{4,9}/[-._;()/:A-Z0-9]+$`)
	pmcidPattern = regexp.MustCompile(`(?i)^PMC[0-9]+$`)
)

type Input struct {
	DOI      string
	PMCID    string
	MaxBytes int64
}

type Result struct {
	Available         bool   `json:"available"`
	RecordAvailable   bool   `json:"recordAvailable,omitempty"`
	SourceUnavailable bool   `json:"sourceUnavailable,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	DOI               string `json:"doi,omitempty"`
	PMCID             string `json:"pmcid,omitempty"`
	PMID              string `json:"pmid,omitempty"`
	Title             string `json:"title,omitempty"`
	AbstractText      string `json:"abstractText,omitempty"`
	AuthorString      string `json:"authorString,omitempty"`
	PublicationDate   string `json:"publicationDate,omitempty"`
	JournalTitle      string `json:"journalTitle,omitempty"`
	Source            string `json:"source"`
	SourceURL         string `json:"sourceUrl"`
	StatusCode        int    `json:"statusCode"`
	ContentType       string `json:"contentType"`
	Body              string `json:"body"`
	BytesRead         int    `json:"bytesRead,omitempty"`
	Complete          bool   `json:"complete,omitempty"`
	EvidenceState     string `json:"evidenceState,omitempty"`
	RecordDepth       string `json:"recordDepth,omitempty"`
}

type Options struct {
	BaseURL                   string
	HTTPClient                *http.Client
	TrustedProxyURL           string
	Resolver                  AddressResolver
	Timeout                   time.Duration
	MaxBytes                  int64
	TestOnlyAllowCustomOrigin bool
}

type AddressResolver = mcpdirectory.AddressResolver

type Client struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	maxBytes   int64
	configErr  error
}

var errOpenAccessFullTextUnavailable = errors.New("open-access full text is unavailable")

type doiLookupRecord struct {
	DOI, PMCID, PMID, Title, AbstractText, AuthorString, PublicationDate, JournalTitle string
}

type articleSourceHTTPError struct {
	operation  string
	statusCode int
}

func (err articleSourceHTTPError) Error() string {
	return fmt.Sprintf("Europe PMC %s returned status %d", err.operation, err.statusCode)
}

func NewClient(options Options) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	configErr := validateOrigin(baseURL, options.TestOnlyAllowCustomOrigin)
	httpClient, clientErr := newPinnedHTTPClient(options.HTTPClient, options.Resolver, options.TrustedProxyURL)
	if configErr == nil {
		configErr = clientErr
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	return &Client{baseURL: baseURL, httpClient: httpClient, timeout: timeout, maxBytes: maxBytes, configErr: configErr}
}

func (c *Client) Fetch(ctx context.Context, input Input) (Result, error) {
	if c.configErr != nil {
		return Result{}, c.configErr
	}
	doi, pmcid, err := normalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var resolved doiLookupRecord
	if pmcid == "" {
		record, resolveErr := c.resolveDOI(requestCtx, doi)
		err = resolveErr
		if err != nil {
			if errors.Is(err, errOpenAccessFullTextUnavailable) {
				return Result{
					Available: false, Status: "not_available", Reason: "no_open_access_full_text",
					DOI: doi, Source: "Europe PMC", SourceURL: c.doiSearchURL(doi), StatusCode: http.StatusOK,
				}, nil
			}
			if ctx.Err() == nil && transientArticleSourceError(err) {
				return unavailableArticleSource(doi, "", c.doiSearchURL(doi), err), nil
			}
			return Result{}, err
		}
		if record.PMCID == "" {
			recordAvailable := strings.TrimSpace(record.AbstractText) != ""
			status, evidenceState, recordDepth := "not_available", "", ""
			if recordAvailable {
				status, evidenceState, recordDepth = "record_available", "abstract-record-read", "abstract_record"
			}
			return Result{
				Available: false, RecordAvailable: recordAvailable,
				Status: status, Reason: "no_open_access_full_text",
				DOI: firstNonEmptyArticleValue(record.DOI, doi), PMID: record.PMID,
				Title: record.Title, AbstractText: record.AbstractText, AuthorString: record.AuthorString,
				PublicationDate: record.PublicationDate, JournalTitle: record.JournalTitle,
				Source: "Europe PMC", SourceURL: c.doiSearchURL(doi), StatusCode: http.StatusOK,
				EvidenceState: evidenceState, RecordDepth: recordDepth,
			}, nil
		}
		pmcid, doi = record.PMCID, firstNonEmptyArticleValue(record.DOI, doi)
		resolved = record
	}
	limit := input.MaxBytes
	if limit <= 0 || limit > c.maxBytes {
		limit = c.maxBytes
	}
	result, err := c.fetchPMCID(requestCtx, doi, pmcid, limit)
	if err != nil && ctx.Err() == nil && transientArticleSourceError(err) {
		return mergeResolvedArticleRecord(unavailableArticleSource(
			doi, pmcid, c.baseURL+"/"+url.PathEscape(pmcid)+"/fullTextXML", err,
		), resolved), nil
	}
	return mergeResolvedArticleRecord(result, resolved), err
}

// Resolving an identifier and acquiring its full text are separate successful
// operations. Preserve the structured record in both success and recovery
// results rather than discarding it when the XML request replaces the result.
func mergeResolvedArticleRecord(result Result, record doiLookupRecord) Result {
	result.DOI = firstNonEmptyArticleValue(result.DOI, record.DOI)
	result.PMCID = firstNonEmptyArticleValue(result.PMCID, record.PMCID)
	result.PMID = firstNonEmptyArticleValue(result.PMID, record.PMID)
	result.Title = firstNonEmptyArticleValue(result.Title, record.Title)
	result.AbstractText = firstNonEmptyArticleValue(result.AbstractText, record.AbstractText)
	result.AuthorString = firstNonEmptyArticleValue(result.AuthorString, record.AuthorString)
	result.PublicationDate = firstNonEmptyArticleValue(result.PublicationDate, record.PublicationDate)
	result.JournalTitle = firstNonEmptyArticleValue(result.JournalTitle, record.JournalTitle)
	result.RecordAvailable = result.RecordAvailable || result.AbstractText != ""
	return result
}

func unavailableArticleSource(doi, pmcid, sourceURL string, err error) Result {
	reason := "temporary_network_error"
	statusCode := 0
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "request_timeout"
	} else {
		var statusError articleSourceHTTPError
		if errors.As(err, &statusError) {
			statusCode = statusError.statusCode
			switch statusCode {
			case http.StatusTooManyRequests:
				reason = "upstream_rate_limited"
			default:
				reason = "upstream_temporarily_unavailable"
			}
		}
	}
	return Result{
		Available: false, SourceUnavailable: true, Retryable: true,
		Status: "source_unavailable", Reason: reason,
		DOI: doi, PMCID: pmcid, Source: "Europe PMC", SourceURL: sourceURL, StatusCode: statusCode,
	}
}

func transientArticleSourceError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var statusError articleSourceHTTPError
	if errors.As(err, &statusError) {
		return statusError.statusCode == http.StatusRequestTimeout ||
			statusError.statusCode == http.StatusTooEarly ||
			statusError.statusCode == http.StatusTooManyRequests ||
			statusError.statusCode >= 500
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func normalizeInput(input Input) (string, string, error) {
	doi := strings.TrimSpace(input.DOI)
	pmcid := strings.TrimSpace(input.PMCID)
	if doi != "" && pmcid != "" {
		return "", "", errors.New("fetch_article_fulltext accepts exactly one of doi or pmcid")
	}
	if doi == "" && pmcid == "" {
		return "", "", errors.New("fetch_article_fulltext requires doi or pmcid")
	}
	if doi != "" {
		lower := strings.ToLower(doi)
		for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "doi:"} {
			if strings.HasPrefix(lower, prefix) {
				doi = doi[len(prefix):]
				break
			}
		}
		doi = strings.TrimSpace(doi)
		if !doiPattern.MatchString(doi) {
			return "", "", errors.New("fetch_article_fulltext.doi is not a valid DOI")
		}
		return doi, "", nil
	}
	pmcid = strings.ToUpper(pmcid)
	if !pmcidPattern.MatchString(pmcid) {
		return "", "", errors.New("fetch_article_fulltext.pmcid must match PMC followed by digits")
	}
	return "", pmcid, nil
}

func (c *Client) resolveDOI(ctx context.Context, doi string) (doiLookupRecord, error) {
	target := c.doiSearchURL(doi)
	response, err := c.do(ctx, target)
	if err != nil {
		return doiLookupRecord{}, fmt.Errorf("resolve DOI through Europe PMC: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return doiLookupRecord{}, articleSourceHTTPError{operation: "DOI lookup", statusCode: response.StatusCode}
	}
	if err := requireMediaType(response.Header.Get("Content-Type"), "application/json"); err != nil {
		return doiLookupRecord{}, fmt.Errorf("Europe PMC DOI lookup: %w", err)
	}
	body, err := readBounded(response.Body, searchMaxBytes)
	if err != nil {
		return doiLookupRecord{}, fmt.Errorf("read Europe PMC DOI lookup: %w", err)
	}
	var payload struct {
		ResultList struct {
			Result []struct {
				PMCID           string `json:"pmcid"`
				PMID            string `json:"pmid"`
				DOI             string `json:"doi"`
				Title           string `json:"title"`
				AbstractText    string `json:"abstractText"`
				AuthorString    string `json:"authorString"`
				PublicationDate string `json:"firstPublicationDate"`
				JournalTitle    string `json:"journalTitle"`
			} `json:"result"`
		} `json:"resultList"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return doiLookupRecord{}, fmt.Errorf("decode Europe PMC DOI lookup: %w", err)
	}
	for _, result := range payload.ResultList.Result {
		pmcid := strings.ToUpper(strings.TrimSpace(result.PMCID))
		if pmcid != "" && !pmcidPattern.MatchString(pmcid) {
			pmcid = ""
		}
		return doiLookupRecord{
			DOI: strings.TrimSpace(result.DOI), PMCID: pmcid, PMID: strings.TrimSpace(result.PMID),
			Title: strings.TrimSpace(result.Title), AbstractText: strings.TrimSpace(result.AbstractText),
			AuthorString: strings.TrimSpace(result.AuthorString), PublicationDate: strings.TrimSpace(result.PublicationDate),
			JournalTitle: strings.TrimSpace(result.JournalTitle),
		}, nil
	}
	return doiLookupRecord{}, fmt.Errorf("%w for DOI %s", errOpenAccessFullTextUnavailable, doi)
}

func firstNonEmptyArticleValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (c *Client) doiSearchURL(doi string) string {
	query := url.Values{}
	query.Set("query", `DOI:"`+doi+`"`)
	query.Set("format", "json")
	query.Set("resultType", "core")
	query.Set("pageSize", "1")
	return c.baseURL + "/search?" + query.Encode()
}

func (c *Client) fetchPMCID(ctx context.Context, doi string, pmcid string, limit int64) (Result, error) {
	target := c.baseURL + "/" + url.PathEscape(pmcid) + "/fullTextXML"
	response, err := c.do(ctx, target)
	if err != nil {
		return Result{}, fmt.Errorf("fetch Europe PMC full text: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Result{
			Available: false, Status: "not_available", Reason: "full_text_not_found",
			DOI: doi, PMCID: pmcid, Source: "Europe PMC", SourceURL: target, StatusCode: response.StatusCode,
		}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, articleSourceHTTPError{operation: "full text", statusCode: response.StatusCode}
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/xml" && mediaType != "text/xml") {
		return Result{}, fmt.Errorf("Europe PMC full text content type %q is not XML", response.Header.Get("Content-Type"))
	}
	if response.ContentLength > limit {
		return Result{}, fmt.Errorf("Europe PMC full text exceeds %d bytes", limit)
	}
	body, err := readBounded(response.Body, limit)
	if err != nil {
		return Result{}, fmt.Errorf("read Europe PMC full text: %w", err)
	}
	return Result{
		Available:     true,
		Status:        "completed",
		DOI:           doi,
		PMCID:         pmcid,
		Source:        "Europe PMC",
		SourceURL:     target,
		StatusCode:    response.StatusCode,
		ContentType:   response.Header.Get("Content-Type"),
		Body:          string(body),
		BytesRead:     len(body),
		Complete:      true,
		EvidenceState: "full-text-read",
		RecordDepth:   "open_access_full_text",
	}, nil
}

func (c *Client) do(ctx context.Context, target string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, application/xml, text/xml")
	identity := buildinfo.Release()
	request.Header.Set("User-Agent", identity.MachineSlug+"-article-fulltext/"+identity.Version)
	return c.httpClient.Do(request)
}

func validateOrigin(raw string, testOnlyCustom bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("article full-text origin must be an HTTPS URL without credentials")
	}
	if !strings.EqualFold(parsed.Hostname(), "www.ebi.ac.uk") {
		return errors.New("article full-text production origin must be www.ebi.ac.uk")
	}
	if !testOnlyCustom && strings.TrimRight(raw, "/") != defaultBaseURL {
		return errors.New("custom article full-text origins require an explicit test-only transport")
	}
	return nil
}

func newPinnedHTTPClient(base *http.Client, resolver AddressResolver, trustedProxyURL string) (*http.Client, error) {
	client, err := mcpdirectory.NewPublicHTTPClient(base, resolver, trustedProxyURL)
	if err != nil {
		return nil, err
	}
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many article full-text redirects")
		}
		return checkRedirect(request, via)
	}
	return client, nil
}

func requireMediaType(raw string, allowed string) error {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || mediaType != allowed {
		return fmt.Errorf("content type %q is not %s", raw, allowed)
	}
	return nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}
