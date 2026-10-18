package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type retiredSkillHTTPTransport func(*http.Request) (*http.Response, error)

func (transport retiredSkillHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestConversationSkillRetiredAPIRejectsOldAuthorPathWithoutCallingModelOrInstalling(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner-retired", "project-retired", "frame-retired")
	root := t.TempDir()
	var calls atomic.Int32
	app := New(Options{Workspace: store, Transcript: repo, FileRoot: root, HTTPClient: &http.Client{Transport: retiredSkillHTTPTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}})
	response := compatJSONRequest(t, app.Handler(), http.MethodPost, "/api/skills/from-conversation", "owner-retired", map[string]any{"conversation_id": "frame-retired"}, http.StatusGone)
	if response["code"] != "conversation_skill_action_required" || response["turn_intent"] != conversationSkillControlIntent {
		t.Fatalf("retired API response=%#v", response)
	}
	compatJSONRequest(t, app.Handler(), http.MethodPost, "/api/skills/from-conversation", "other-owner", map[string]any{"conversation_id": "frame-retired"}, http.StatusNotFound)
	compatJSONRequest(t, app.Handler(), http.MethodPost, "/api/skills/from-conversation", "owner-retired", map[string]any{"conversation_id": "frame-retired", "unexpected": true}, http.StatusBadRequest)
	if calls.Load() != 0 {
		t.Fatal("retired API called an external provider")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "skills")); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatalf("retired API installed files: count=%d err=%v", len(entries), err)
	}
}

func TestConversationSkillRetiredAPIPreservesAuthenticationAndMethodBoundary(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	app := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/skills/from-conversation", strings.NewReader(`{"conversation_id":"frame"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	app.handleSkillFromConversation(response, newLoopbackTestRequest(http.MethodGet, "/api/skills/from-conversation", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("method status=%d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
}
