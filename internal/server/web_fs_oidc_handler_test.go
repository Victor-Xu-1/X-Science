package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"synon-go/internal/persistence/workspace"
)

func TestWebFSOIDCHandlerRequiresSessionAndExplicitHostGrant(t *testing.T) {
	provider := newTestOIDCProvider(t)
	privateRoot := t.TempDir()
	privateFile := filepath.Join(privateRoot, "private.txt")
	writeWebFSTestFile(t, privateFile, "OIDC-owned file content")
	stateRoot := t.TempDir()
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(Options{
		FileRoot:   stateRoot,
		Workspace:  store,
		HTTPClient: provider.server.Client(),
		WebAuth: WebAuthOptions{Google: WebOIDCProviderOptions{
			IssuerURL: provider.server.URL, ClientID: provider.clientID, ClientSecret: provider.clientSecret,
		}},
	})
	// A forged identity has a real grant, so an identity-header bypass would
	// actually expose the file rather than merely select another denied user.
	const forgedUser = "other-granted-user"
	if _, err := app.upsertHostGrant(forgedUser, privateRoot, "read"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: server.Client().Transport,
		Jar:       jar,
		Timeout:   5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	post := func(client *http.Client, route string, payload any, forged string, wantStatus int) []byte {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+route, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", server.URL)
		request.Header.Set("X-Synon-User-Id", forged)
		if client.Jar != nil {
			for _, cookie := range client.Jar.Cookies(baseURL) {
				if cookie.Name == webCSRFCookieName {
					request.Header.Set(webCSRFHeaderName, cookie.Value)
				}
			}
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != wantStatus {
			t.Fatalf("%s status=%d want=%d body=%s", route, response.StatusCode, wantStatus, body)
		}
		return body
	}
	read := map[string]any{"path": privateFile}
	post(server.Client(), "/api/fs/read", read, forgedUser, http.StatusUnauthorized)

	// Use the existing local OIDC protocol fixture, not an injected user header
	// or manually minted session. Start and callback traverse the real Handler.
	loginURL := server.URL + "/api/auth/oidc/google/start"
	for step := 0; step < 3; step++ {
		response, err := client.Get(loginURL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("OIDC step=%d status=%d location=%q", step, response.StatusCode, response.Header.Get("Location"))
		}
		loginURL = response.Header.Get("Location")
	}
	current, err := client.Get(server.URL + "/api/auth/user")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Body.Close()
	var account struct {
		User struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
		} `json:"user"`
	}
	if err := json.NewDecoder(current.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	if current.StatusCode != http.StatusOK || account.User.ID == "" || account.User.ID == forgedUser || account.User.Provider != "google" {
		t.Fatalf("OIDC account status=%d body=%#v", current.StatusCode, account)
	}
	post(client, "/api/fs/read", read, "", http.StatusForbidden)
	post(client, "/api/fs/read", read, forgedUser, http.StatusForbidden)
	post(client, "/api/go/preferences/host-grants", map[string]any{
		"path": privateRoot, "mode": "read",
	}, "", http.StatusOK)
	var content string
	if err := json.Unmarshal(post(client, "/api/fs/read", read, "", http.StatusOK), &content); err != nil {
		t.Fatal(err)
	}
	if content != "OIDC-owned file content" {
		t.Fatalf("granted file content=%q", content)
	}
	// A grant does not turn its owner ID into an authentication credential.
	post(server.Client(), "/api/fs/read", read, account.User.ID, http.StatusUnauthorized)
}
