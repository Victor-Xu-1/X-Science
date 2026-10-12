package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostOperatorRejectsForeignSessionAndForgedIdentityBeforeGlobalWork(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir(), XScienceLinkAuth: XScienceLinkAuthOptions{
		Username: "configured-operator", Password: "fixture-operator-password", UserID: "operator-id",
	}})
	foreignToken, _, err := srv.xscienceLinkAuth.createSessionForUser(xscienceLinkAuthUser{
		ID: "other-account", Username: "other-account",
	}, xscienceSessionScopeWeb)
	if err != nil {
		t.Fatal(err)
	}
	operatorToken, _, err := srv.xscienceLinkAuth.LoginForScope("configured-operator", "fixture-operator-password", xscienceSessionScopeWeb)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token string
		want        bool
	}{
		{"foreign-session", foreignToken, false},
		{"header-only", "", false},
		{"configured-operator", operatorToken, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newLoopbackTestRequest(http.MethodPost, "/api/go/preferences/host-grants", strings.NewReader(`{}`))
			request.Header.Set("X-Science-User-Id", "operator-id")
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			if allowed := srv.requireHostOperator(response, request); allowed != tc.want {
				t.Fatalf("allowed=%v want=%v", allowed, tc.want)
			}
			if !tc.want && (response.Code != http.StatusForbidden || !bytes.Contains(response.Body.Bytes(), []byte("HOST_OPERATOR_REQUIRED"))) {
				t.Fatalf("operator guard=%d %s", response.Code, response.Body.String())
			}
		})
	}
	for _, target := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/go/preferences/host-grants"},
		{http.MethodPatch, "/api/go/preferences/host-grants"},
		{http.MethodPost, "/api/go/preferences/host-grants/picker"},
		{http.MethodPost, "/api/settings/data-dir"},
		{http.MethodDelete, "/api/settings/data-dir/last-move"},
		{http.MethodPost, "/api/skills/untrusted/edit"},
		{http.MethodPost, "/api/compute/ssh-hosts"},
		{http.MethodGet, "/api/compute/ssh-config-aliases"},
	} {
		request := newLoopbackTestRequest(target.method, target.path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+foreignToken)
		request.Header.Set("X-Science-User-Id", "operator-id")
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || !bytes.Contains(response.Body.Bytes(), []byte("HOST_OPERATOR_REQUIRED")) {
			t.Fatalf("%s %s=%d %s", target.method, target.path, response.Code, response.Body.String())
		}
	}
}

func TestHostOperatorKeepsPasswordlessLoopbackButRejectsRemotePeer(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	request := newLoopbackTestRequest(http.MethodPost, "/", nil)
	if !srv.requireHostOperator(httptest.NewRecorder(), request) {
		t.Fatal("passwordless local operator rejected")
	}
	request.RemoteAddr = "192.0.2.10:1234"
	if srv.requireHostOperator(httptest.NewRecorder(), request) {
		t.Fatal("unauthenticated remote peer acquired operator capability")
	}
}
