package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/tools/rcsbfiles"
	"x-science/internal/tools/rcsbsearch"
)

func TestServerRCSBClientsRespectExplicitProxyAuthority(t *testing.T) {
	// Stop direct DNS at a controlled boundary. No RCSB request leaves the test;
	// an explicit proxy must receive CONNECT without relying on the host resolver.
	var dnsCalls atomic.Int64
	previousResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dnsCalls.Add(1)
			return nil, errors.New("controlled DNS unavailable")
		},
	}
	t.Cleanup(func() { net.DefaultResolver = previousResolver })
	for _, inherited := range []bool{false, true} {
		name := "explicit"
		if inherited {
			name = "inherited"
		}
		t.Run(name, func(t *testing.T) {
			var proxyCalls atomic.Int64
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				proxyCalls.Add(1)
				if request.Method != http.MethodConnect ||
					(request.Host != "files.rcsb.org:443" && request.Host != "search.rcsb.org:443") {
					t.Errorf("unexpected proxy destination: %s %s", request.Method, request.Host)
				}
				http.Error(w, "controlled proxy failure", http.StatusServiceUnavailable)
			}))
			defer proxy.Close()
			t.Setenv("HTTPS_PROXY", proxy.URL)
			app := New(Options{FileRoot: t.TempDir(), ConfigNetworkProxy: proxy.URL,
				ConfigNetworkProxyInherited: inherited})
			t.Cleanup(func() { _ = app.Close(context.Background()) })
			for operation, execute := range map[string]func(context.Context) error{
				"download": func(ctx context.Context) error {
					result, err := app.rcsbFiles.Fetch(ctx, rcsbfiles.Input{
						ResourceKind: rcsbfiles.EntryCoordinates, EntryID: "1CRN", Format: rcsbfiles.FormatCIF,
					})
					if result.Body != nil {
						result.Body.Close()
					}
					return err
				},
				"search": func(ctx context.Context) error {
					_, err := app.rcsbSearch.Search(ctx, rcsbsearch.Input{Query: "crambin", Limit: 1})
					return err
				},
			} {
				t.Run(operation, func(t *testing.T) {
					beforeProxy, beforeDNS := proxyCalls.Load(), dnsCalls.Load()
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					if err := execute(ctx); err == nil {
						t.Fatal("expected controlled routing failure")
					}
					if inherited {
						if proxyCalls.Load() != beforeProxy || dnsCalls.Load() == beforeDNS {
							t.Fatal("inherited route acquired public-source proxy authority")
						}
					} else if proxyCalls.Load() != beforeProxy+1 || dnsCalls.Load() != beforeDNS {
						t.Fatal("explicit RCSB proxy was lost or silently retried direct")
					}
				})
			}
		})
	}
}

func TestServerRCSBClientsPreserveConfiguredImplementations(t *testing.T) {
	files, search := &agentRCSBFetcher{}, &agentRCSBSearcher{}
	app := New(Options{FileRoot: t.TempDir(), ConfigNetworkProxy: "http://127.0.0.1:9",
		RCSBFiles: files, RCSBSearch: search})
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	if app.rcsbFiles != files || app.rcsbSearch != search {
		t.Fatal("default proxy configuration replaced an injected RCSB implementation")
	}
}
