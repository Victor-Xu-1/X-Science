package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompatibilityHostGrantMutationsUseCanonicalOperatorBoundary(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, "PICKER"} {
		for _, operator := range []bool{false, true} {
			name := method + "/foreign"
			if operator {
				name = method + "/operator"
			}
			t.Run(name, func(t *testing.T) {
				pickerCalls := 0
				root := t.TempDir()
				app := New(Options{FileRoot: t.TempDir(), XScienceLinkAuth: XScienceLinkAuthOptions{
					Username: "fixture-operator", Password: "fixture-operator-password", UserID: "operator-id",
				}, HostDirectoryPicker: func(context.Context) (string, error) { pickerCalls++; return root, nil }})
				t.Cleanup(func() { closeTestServer(t, app) })
				owner := "foreign-id"
				token, _, err := app.xscienceLinkAuth.createSessionForUser(xscienceLinkAuthUser{ID: owner, Username: "fixture-foreign"}, xscienceSessionScopeWeb)
				if operator {
					owner = "operator-id"
					token, _, err = app.xscienceLinkAuth.LoginForScope("fixture-operator", "fixture-operator-password", xscienceSessionScopeWeb)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := app.upsertHostGrant(owner, root, "read"); err != nil {
					t.Fatal(err)
				}
				path := "/api/preferences/host-grants"
				body := map[string]any{"path": root, "mode": "rw"}
				verb := method
				if method == "PICKER" {
					verb = http.MethodPost
					path += "/picker"
					delete(body, "path")
				}
				response := authenticatedHostGrantFixtureRequest(t, app, token, verb, path, body)
				wantStatus := http.StatusForbidden
				wantMode := "read"
				if operator {
					wantStatus = http.StatusOK
					wantMode = "read_write"
				}
				if response.Code != wantStatus {
					t.Errorf("mutation status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
				}
				if !operator && !bytes.Contains(response.Body.Bytes(), []byte("HOST_OPERATOR_REQUIRED")) {
					t.Errorf("mutation did not use canonical operator rejection: %s", response.Body.String())
				}
				wantPickerCalls := 0
				if operator && method == "PICKER" {
					wantPickerCalls = 1
				}
				if pickerCalls != wantPickerCalls {
					t.Errorf("picker calls=%d want=%d", pickerCalls, wantPickerCalls)
				}
				grants, err := app.loadHostGrants(owner)
				if err != nil || len(grants) != 1 || grants[0].Path != root || grants[0].Mode != wantMode {
					t.Errorf("mutation grants=%#v err=%v wantMode=%s", grants, err, wantMode)
				}
				listed := authenticatedHostGrantFixtureRequest(t, app, token, http.MethodGet, "/api/preferences/host-grants", nil)
				if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte("hostPath")) {
					t.Errorf("owner cannot list granted roots: %d %s", listed.Code, listed.Body.String())
				}
				revoked := authenticatedHostGrantFixtureRequest(t, app, token, http.MethodDelete, "/api/preferences/host-grants", map[string]any{"path": root})
				if revoked.Code != http.StatusOK {
					t.Errorf("owner cannot revoke own root: %d %s", revoked.Code, revoked.Body.String())
				}
				if grants, err := app.loadHostGrants(owner); err != nil || len(grants) != 0 {
					t.Errorf("owner revoke did not persist: %#v %v", grants, err)
				}
			})
		}
	}
}

func authenticatedHostGrantFixtureRequest(t *testing.T, app *Server, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := newLoopbackTestRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Science-User-Id", "operator-id") // A forged header cannot change the authenticated account.
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}
