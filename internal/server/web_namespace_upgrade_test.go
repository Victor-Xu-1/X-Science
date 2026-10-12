package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestWebNamespaceCookieUpgradeAuthenticatesExistingLedgerOnly(t *testing.T) {
	store, err := openWebSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, _, err := store.Create("owner", "local", true, webSessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	app := &Server{}
	app.webSessions = store
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/auth/user", nil)
	request.AddCookie(&http.Cookie{Name: "synon_session", Value: token})
	request.AddCookie(&http.Cookie{Name: "synon_csrf", Value: csrf})
	request.AddCookie(&http.Cookie{Name: "synon_device", Value: "opaque-device"})
	request.AddCookie(&http.Cookie{Name: "unrelated", Value: "keep"})
	response := httptest.NewRecorder()
	upgraded := app.upgradeWebCookieNamespace(response, request)
	if webSessionCookieToken(upgraded) != token {
		t.Fatal("authenticated opaque token was not transferred")
	}
	if _, err := upgraded.Cookie("synon_session"); err == nil {
		t.Fatal("legacy cookie remains in canonical request")
	}
	if cookie, err := upgraded.Cookie("unrelated"); err != nil || cookie.Value != "keep" {
		t.Fatal("unrelated cookie changed")
	}
	expired, written := 0, 0
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge < 0 {
			expired++
		} else {
			written++
		}
	}
	if expired != 3 || written != 3 {
		t.Fatal("canonical cookie transfer did not retire exact legacy keys")
	}
	if app.upgradeWebCookieNamespace(httptest.NewRecorder(), upgraded) != upgraded {
		t.Fatal("cookie upgrade was not idempotent")
	}
	_, found, err := store.Authenticate(token, false)
	if err != nil || !found {
		t.Fatal("namespace transfer invalidated original ledger session")
	}
}

func TestWebNamespaceDualAccountCookiesCannotRestoreLoginAfterLogout(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(Options{FileRoot: root, Workspace: store, XScienceLinkAuth: XScienceLinkAuthOptions{
		Username: "current", Password: "disposable-test-password", UserID: "current-owner",
	}})
	oldToken, oldCSRF, _, err := app.webSessions.Create("previous-owner", "local", true, webSessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	currentToken, currentCSRF, _, err := app.webSessions.Create("current-owner", "local", false, webSessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	address, _ := url.Parse("http://localhost/")
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(address, []*http.Cookie{
		{Name: "synon_session", Value: oldToken}, {Name: "synon_csrf", Value: oldCSRF}, {Name: "synon_device", Value: "old-device"},
		{Name: webSessionCookieName, Value: currentToken}, {Name: webCSRFCookieName, Value: currentCSRF},
	})
	request := httptest.NewRequest(http.MethodPost, "http://localhost/logout", strings.NewReader(`{}`))
	for _, cookie := range jar.Cookies(address) {
		request.AddCookie(cookie)
	}
	request.Header.Set(webCSRFHeaderName, currentCSRF)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", response.Code, response.Body.String())
	}
	jar.SetCookies(address, response.Result().Cookies())
	after := httptest.NewRequest(http.MethodGet, "http://localhost/api/auth/user", nil)
	for _, cookie := range jar.Cookies(address) {
		after.AddCookie(cookie)
	}
	for _, name := range append(previousWebCookieNames[:], webSessionCookieName, webCSRFCookieName) {
		if _, err := after.Cookie(name); err == nil {
			t.Fatalf("logout retained %s", name)
		}
	}
	if webSessionCookieToken(app.upgradeWebCookieNamespace(httptest.NewRecorder(), after)) != "" {
		t.Fatal("explicit logout restored the previous account")
	}
	if _, valid, err := app.webSessions.Authenticate(currentToken, false); err != nil || valid {
		t.Fatal("current session was not revoked")
	}
	if _, valid, err := app.webSessions.Authenticate(oldToken, false); err != nil || !valid {
		t.Fatal("unselected owner's session was mutated")
	}
}

func TestWebNamespaceNewSessionAndSecurityClearsRetirePriorCookies(t *testing.T) {
	app := &Server{}
	for _, action := range []string{"login", "security-clear"} {
		t.Run(action, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			response := httptest.NewRecorder()
			if action == "login" {
				app.writeWebSessionCookies(response, request, "new-token", "new-csrf", "new-device", false, "")
			} else {
				app.clearWebAuthCookies(response, request)
			}
			retired := map[string]bool{}
			for _, cookie := range response.Result().Cookies() {
				if cookie.MaxAge < 0 {
					retired[cookie.Name] = true
				}
			}
			for _, name := range previousWebCookieNames {
				if !retired[name] {
					t.Fatalf("%s retained %s", action, name)
				}
			}
		})
	}
}

func TestWebNamespaceCookieUpgradeRejectsInvalidRevokedAndConflictingTokens(t *testing.T) {
	store, err := openWebSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, session, err := store.Create("owner", "local", false, webSessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	app := &Server{}
	app.webSessions = store
	for _, scenario := range []struct{ token, csrf, canonical string }{
		{"invalid", csrf, ""}, {token, "wrong", ""}, {token, csrf, "canonical-owner"},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
		request.AddCookie(&http.Cookie{Name: "synon_session", Value: scenario.token})
		request.AddCookie(&http.Cookie{Name: "synon_csrf", Value: scenario.csrf})
		if scenario.canonical != "" {
			request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: scenario.canonical})
		}
		response := httptest.NewRecorder()
		if app.upgradeWebCookieNamespace(response, request) != request || len(response.Result().Cookies()) != 0 {
			t.Fatal("unverified or conflicting cookie authority was modified")
		}
	}
	if _, _, err := store.Revoke("owner", session.ID, token); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	request.AddCookie(&http.Cookie{Name: "synon_session", Value: token})
	request.AddCookie(&http.Cookie{Name: "synon_csrf", Value: csrf})
	response := httptest.NewRecorder()
	if app.upgradeWebCookieNamespace(response, request) != request || len(response.Result().Cookies()) != 0 {
		t.Fatal("revoked session was revived by namespace upgrade")
	}
}
