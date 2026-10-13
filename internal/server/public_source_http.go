package server

import (
	"context"
	"net/http"

	"x-science/internal/mcpdirectory"
)

// Connector execution, discovery and browser source consumers must not lose
// the operator route when they construct their per-request public client.
func (s *Server) publicSourceHTTPClient(ctx context.Context, rawURL string, base *http.Client) (*http.Client, error) {
	return mcpdirectory.SecureHTTPClientWithProxy(ctx, rawURL, base, s.configNetworkProxy)
}

func (s *Server) validatePublicSourceURL(ctx context.Context, rawURL string) error {
	_, err := s.publicSourceHTTPClient(ctx, rawURL, s.httpClient)
	return err
}
