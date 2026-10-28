package literaturediscovery

import (
	"regexp"
	"strings"
)

var (
	pmidPrefix   = regexp.MustCompile(`(?i)^PMID\s*:\s*`)
	pmidURL      = regexp.MustCompile(`(?i)^https?://(?:www\.)?pubmed\.ncbi\.nlm\.nih\.gov/(\d+)/?(?:[?#].*)?$`)
	pmcidPrefix  = regexp.MustCompile(`(?i)^PMCID\s*:\s*`)
	pmcidURL     = regexp.MustCompile(`(?i)^https?://(?:pmc\.ncbi\.nlm\.nih\.gov/articles/(PMC\d+)|(?:www\.)?ncbi\.nlm\.nih\.gov/pmc/articles/(PMC\d+))/?(?:[?#].*)?$`)
	pmcidValue   = regexp.MustCompile(`(?i)^(?:PMC)?(\d+)$`)
	digitsValue  = regexp.MustCompile(`^\d+$`)
	arxivPrefix  = regexp.MustCompile(`(?i)^arxiv:\s*`)
	arxivURL     = regexp.MustCompile(`(?i)^https?://(?:www\.)?arxiv\.org/(?:abs|pdf)/`)
	arxivSuffix  = regexp.MustCompile(`(?i)(?:\.pdf|v\d+)$`)
	arxivValue   = regexp.MustCompile(`(?i)^(?:\d{4}\.\d{4,5}|[a-z][a-z.-]*/\d{7})$`)
	paperIDValue = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
)

// NormalizeIdentifier accepts only supported bibliographic identity schemes.
// Invalid metadata cannot become a provider path or silently authorize a match.
func NormalizeIdentifier(input Identifier) (Identifier, bool) {
	scheme := strings.ToLower(strings.TrimSpace(input.Scheme))
	value := strings.TrimSpace(input.Value)
	if len(value) == 0 || len(value) > 100000 {
		return Identifier{}, false
	}
	valid := false
	switch scheme {
	case "doi":
		value, valid = NormalizeDOI(value)
	case "pmid":
		value = pmidPrefix.ReplaceAllString(value, "")
		if match := pmidURL.FindStringSubmatch(value); match != nil {
			value = match[1]
		}
		valid = digitsValue.MatchString(value)
	case "pmcid":
		value = pmcidPrefix.ReplaceAllString(value, "")
		if match := pmcidURL.FindStringSubmatch(value); match != nil {
			value = match[1] + match[2]
		}
		if match := pmcidValue.FindStringSubmatch(value); match != nil {
			value, valid = "PMC"+match[1], true
		}
	case "arxiv":
		value = arxivURL.ReplaceAllString(arxivPrefix.ReplaceAllString(value, ""), "")
		if index := strings.IndexAny(value, "?#"); index >= 0 {
			value = value[:index]
		}
		// A URL can contain both the version and .pdf suffix.
		value = arxivSuffix.ReplaceAllString(value, "")
		value = arxivSuffix.ReplaceAllString(value, "")
		value = strings.ToLower(strings.TrimSpace(value))
		valid = arxivValue.MatchString(value)
	}
	if !valid || len(value) > 4096 {
		return Identifier{}, false
	}
	return Identifier{Scheme: scheme, Value: value}, true
}

func exactProviderID(reference Reference) string {
	for _, scheme := range []string{"doi", "pmid", "pmcid", "arxiv"} {
		values := make(map[string]struct{})
		for _, candidate := range reference.Identifiers {
			identifier, valid := NormalizeIdentifier(candidate)
			if valid && identifier.Scheme == scheme {
				values[identifier.Value] = struct{}{}
			}
		}
		if len(values) == 1 {
			for value := range values {
				if scheme == "pmcid" {
					value = strings.TrimPrefix(value, "PMC")
				}
				return strings.ToUpper(scheme) + ":" + value
			}
		}
	}
	if paperIDValue.MatchString(reference.SemanticScholarID) {
		return strings.ToLower(reference.SemanticScholarID)
	}
	return ""
}
