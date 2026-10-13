package server

import (
	"context"
	"net/http"
	"strings"

	"x-science/internal/mcpdirectory"
	"x-science/internal/tools/websearch"
)

// Every public-source consumer shares the operator route. Supplying a base
// HTTP client alone must not turn its ambient proxy into DNS authority.
func configurePublicSourceOptions(options *Options) (websearch.Options, string) {
	proxy := strings.TrimSpace(options.ConfigNetworkProxy)
	if options.ConfigNetworkProxyInherited {
		proxy = ""
	}
	options.WebFetchOptions.TrustedProxyURL = proxy
	if options.WebFetchOptions.BaseHTTPClient == nil {
		options.WebFetchOptions.BaseHTTPClient = options.HTTPClient
	}
	search := options.WebSearchOptions
	search.TrustedProxyURL = proxy
	if search.BaseHTTPClient == nil {
		search.BaseHTTPClient = options.HTTPClient
	}
	if strings.TrimSpace(search.Root) == "" {
		search.Root = options.FileRoot
	}
	return search, proxy
}

// Connector execution, discovery and browser source consumers must not lose
// the operator route when they construct their per-request public client.
func (s *Server) publicSourceHTTPClient(ctx context.Context, rawURL string, base *http.Client) (*http.Client, error) {
	return mcpdirectory.SecureHTTPClientWithProxy(ctx, rawURL, base, s.publicSourceProxy)
}

func (s *Server) validatePublicSourceURL(ctx context.Context, rawURL string) error {
	_, err := s.publicSourceHTTPClient(ctx, rawURL, s.httpClient)
	return err
}
