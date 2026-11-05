package agentruntime

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

type transientSourceFailureIdentity struct {
	bucket      string
	destination string
}

type transientSourceFailureCount struct {
	epoch    uint64
	attempts int
}

// Owned and serialized by the existing per-Run failed-call guard. Only actual
// sources accrue failures; submitted locators retain their observed association
// so legacy gateways cannot lose the budget through post-execution aliases.
type transientSourceFailureBudget struct {
	sources  map[string]transientSourceFailureCount
	origins  map[string]map[string]struct{}
	locators map[string]string
}

func transientSourceFailureIdentityFor(call ToolCall, capabilities []string) transientSourceFailureIdentity {
	return transientSourceFailureIdentity{
		bucket:      transientSourceFailureBucket(call, capabilities),
		destination: failedToolCallSourceDestination(call, capabilities),
	}
}

func (b *transientSourceFailureBudget) count(bucket string, epoch uint64) int {
	if counter := b.sources[bucket]; counter.epoch == epoch {
		return counter.attempts
	}
	return 0
}

func (b *transientSourceFailureBudget) attempts(source transientSourceFailureIdentity, epoch uint64) int {
	if source.bucket == "" {
		return 0
	}
	if actual, known := b.locators[source.destination]; source.destination != "" && known {
		return b.count(actual, epoch)
	}
	attempts := b.count(source.bucket, epoch)
	// An unobserved path/query cannot prove a new actual authority behind an
	// exhausted alias. Known locators above still select their own actual source.
	for actual := range b.origins[source.bucket] {
		if count := b.count(actual, epoch); count > attempts {
			attempts = count
		}
	}
	return attempts
}

func (b *transientSourceFailureBudget) remember(submitted, actual transientSourceFailureIdentity) {
	if submitted.bucket == "" || actual.bucket == "" || submitted.destination == "" {
		return
	}
	if b.locators == nil {
		b.locators = make(map[string]string)
	}
	b.locators[submitted.destination] = actual.bucket
	if submitted.bucket != actual.bucket {
		if b.origins == nil {
			b.origins = make(map[string]map[string]struct{})
		}
		if b.origins[submitted.bucket] == nil {
			b.origins[submitted.bucket] = make(map[string]struct{})
		}
		b.origins[submitted.bucket][actual.bucket] = struct{}{}
	}
}

func (b *transientSourceFailureBudget) failed(submitted, actual transientSourceFailureIdentity, epoch uint64) int {
	b.remember(submitted, actual)
	if b.sources == nil {
		b.sources = make(map[string]transientSourceFailureCount)
	}
	counter := b.sources[actual.bucket]
	if counter.epoch != epoch {
		counter = transientSourceFailureCount{epoch: epoch}
	}
	counter.attempts++
	b.sources[actual.bucket] = counter
	return counter.attempts
}

func (b *transientSourceFailureBudget) succeeded(submitted, actual transientSourceFailureIdentity) {
	b.remember(submitted, actual)
	// A healthy actual source proves nothing about a different source sharing
	// the same submitted alias. Its failure constraint must survive this read.
	delete(b.sources, actual.bucket)
}

// Locator reads have independent upstream failure domains. Keep the existing
// connector budget for tools without an actual HTTP(S) locator; path, query,
// fragment and presentation changes never create a fresh source budget.
func transientSourceFailureBucket(call ToolCall, capabilities []string) string {
	bucket := transientSourceConnectorBucket(call.Name, capabilities)
	if bucket == "" {
		return ""
	}
	origin := failedToolCallSourceOrigin(failedToolCallSourceURL(call, capabilities))
	if origin != "" {
		return bucket + "\x00" + origin
	}
	return bucket
}

// This is identity normalization, not URL admission or a network probe. The
// existing gateway remains responsible for permissions, redirects and SSRF.
func failedToolCallSourceOrigin(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Opaque != "" || parsed.User != nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return ""
	}
	if address := net.ParseIP(host); address != nil {
		host = address.String()
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	} else {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return ""
		}
		port = strconv.Itoa(number)
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
