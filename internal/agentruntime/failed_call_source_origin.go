package agentruntime

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

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
