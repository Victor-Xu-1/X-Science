package mcpstdio

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestPublicTransportMarkerAloneCannotDelegateDNS(t *testing.T) {
	for _, resolution := range []string{"unavailable", "private"} {
		t.Run(resolution, func(t *testing.T) {
			calls := 0
			ctx := WithHTTPClient(context.Background(), &http.Client{Transport: pinnedRoundTripperStub{}})
			ctx = withOAuthResolver(ctx, oauthResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
				calls++
				if resolution == "unavailable" {
					return nil, errors.New("DNS unavailable")
				}
				return []net.IPAddr{{IP: net.ParseIP("10.0.0.8")}}, nil
			}))
			if _, err := secureRemoteHTTPClient(ctx, "https://marker.example.com/mcp"); err == nil {
				t.Fatal("marker-only MCP transport bypassed DNS policy")
			}
			if err := validateResolvedOAuthEndpoint(ctx, "https://marker.example.com/authorize", "authorization"); err == nil {
				t.Fatal("marker-only authorization link bypassed DNS policy")
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://marker.example.com/metadata", nil)
			if _, err := doOAuthRequest(ctx, request); err == nil {
				t.Fatal("marker-only OAuth request bypassed DNS policy")
			}
			if calls != 3 {
				t.Fatalf("DNS calls=%d, want one for each guarded entry", calls)
			}
		})
	}
}

type denyingPublicTransport struct{ pinnedRoundTripperStub }

func (denyingPublicTransport) ValidatePublicHTTPSDestination(context.Context, string) error {
	return errors.New("public transport policy rejected destination")
}

func TestPublicTransportDestinationRejectionNeverFallsBack(t *testing.T) {
	ctx := WithHTTPClient(context.Background(), &http.Client{Transport: denyingPublicTransport{}})
	ctx = withOAuthResolver(ctx, oauthResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("rejected transport policy fell back to local DNS")
		return nil, nil
	}))
	if _, err := secureRemoteHTTPClient(ctx, "https://source.example.com/mcp"); err == nil || !strings.Contains(err.Error(), "policy rejected") {
		t.Fatalf("remote policy rejection lost: %v", err)
	}
	if err := validateResolvedOAuthEndpoint(ctx, "https://source.example.com/authorize", "authorization"); err == nil || !strings.Contains(err.Error(), "policy rejected") {
		t.Fatalf("OAuth policy rejection lost: %v", err)
	}
}
