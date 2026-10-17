package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const conversationSkillReturnToolForTest = "return_conversation_skill"

func TestConversationSkillAuthoringRequestsTypedDraftAndInstalls(t *testing.T) {
	arguments := conversationSkillDraftArgumentsForTest(t, nil)
	fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{
		conversationSkillToolReplyForTest(arguments),
	})
	before, err := fixture.server.loadConversationSkillMessages(context.Background(), "local", fixture.frameID)
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.save(context.Background())
	if response.Code != http.StatusCreated {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body.String())
	}
	requests := fixture.capturedRequests()
	if len(requests) != 1 {
		t.Fatalf("authoring requests=%d want=1", len(requests))
	}
	assertConversationSkillDraftRequest(t, requests[0])
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["name"] != "typed-fixture-workflow" || result["scope"] != "personal" || result["source"] != "server-authoring" {
		t.Fatalf("save result=%#v", result)
	}
	installed := filepath.Join(fixture.root, "skills", "typed-fixture-workflow")
	markdown, err := os.ReadFile(filepath.Join(installed, "SKILL.md"))
	if err != nil || !bytes.Contains(markdown, []byte("Preserve \"quoted input\"")) {
		t.Fatalf("installed manifest=%q err=%v", markdown, err)
	}
	if _, err := os.Stat(filepath.Join(installed, "kernel.py")); err != nil {
		t.Fatalf("installed helper: %v", err)
	}
	listed := compatJSONArrayRequest(t, fixture.server.Handler(), http.MethodGet, "/api/skills", "local", nil, http.StatusOK)
	listedPersonal := false
	for _, entry := range listed {
		if entry["name"] == "typed-fixture-workflow" {
			listedPersonal = entry["source"] == "custom" && entry["is_custom"] == true
		}
	}
	if !listedPersonal {
		t.Fatal("saved Skill is not listed as personal/custom")
	}
	search, err := fixture.server.executeSkillSearchTool(map[string]any{"query": "select:typed-fixture-workflow"})
	if err != nil {
		t.Fatal(err)
	}
	if matches := search.(map[string]any)["matches"]; !reflect.DeepEqual(matches, []string{"typed-fixture-workflow"}) {
		t.Fatalf("saved Skill search=%#v", matches)
	}
	loaded, err := fixture.server.executeSkillTool(map[string]any{"skill": "typed-fixture-workflow"})
	if err != nil {
		t.Fatal(err)
	}
	data := loaded.(map[string]any)["data"].(map[string]any)
	if data["loaded"] != true || data["executed"] != false {
		t.Fatalf("Skill load must not claim helper execution: %#v", data)
	}
	after, err := fixture.server.loadConversationSkillMessages(context.Background(), "local", fixture.frameID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("saving/loading changed the conversation: before=%#v after=%#v err=%v", before, after, err)
	}
	restarted := New(Options{Workspace: fixture.server.workspaceStore, FileRoot: fixture.root})
	if _, found := findCatalogSkill(restarted.skillCatalog, "typed-fixture-workflow"); !found {
		t.Fatal("saved Skill did not survive catalog reconstruction")
	}
}

func TestConversationSkillAuthoringCorrectsFormatOnlyOnce(t *testing.T) {
	valid := conversationSkillDraftArgumentsForTest(t, nil)
	changed := func(edit func(map[string]any)) string {
		return conversationSkillDraftArgumentsForTest(t, edit)
	}
	noCall := func(content string) conversationSkillAuthoringReply {
		return conversationSkillAuthoringReply{message: map[string]any{"role": "assistant", "content": content}}
	}
	multiple := conversationSkillToolReplyForTest(valid)
	multiple.message["tool_calls"] = []any{
		conversationSkillToolCallForTest("draft-one", conversationSkillReturnToolForTest, valid),
		conversationSkillToolCallForTest("draft-two", conversationSkillReturnToolForTest, valid),
	}
	wrongName := conversationSkillToolReplyForTest(valid)
	wrongName.message["tool_calls"] = []any{conversationSkillToolCallForTest("draft-one", "other_draft_return", valid)}
	cases := []struct {
		name  string
		reply conversationSkillAuthoringReply
	}{
		{"prose_json", noCall("Here is the draft:\n" + changed(func(fields map[string]any) { fields["name"] = "must-not-install" }))},
		{"no_call", noCall("The draft response is not structured.")},
		{"missing_field", conversationSkillToolReplyForTest(changed(func(fields map[string]any) { delete(fields, "kernel_py") }))},
		{"nonstring_field", conversationSkillToolReplyForTest(changed(func(fields map[string]any) { fields["description"] = 17 }))},
		{"null_field", conversationSkillToolReplyForTest(changed(func(fields map[string]any) { fields["description"] = nil }))},
		{"unknown_field", conversationSkillToolReplyForTest(changed(func(fields map[string]any) { fields["unexpected"] = "reject" }))},
		{"multiple_calls", multiple},
		{"wrong_call_name", wrongName},
		{"provider_arguments_diagnostic", conversationSkillToolReplyForTest("{\"name\":not_json}")},
		{"duplicate_field", conversationSkillToolReplyForTest("{\"name\":\"duplicate-name\"," + strings.TrimPrefix(valid, "{"))},
		{"invalid_name", conversationSkillToolReplyForTest(changed(func(fields map[string]any) { fields["name"] = "../escape" }))},
		{"invalid_manifest", conversationSkillToolReplyForTest(changed(func(fields map[string]any) {
			fields["skill_markdown"] = "---\nname: [unterminated\n---\nFixture body."
		}))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{
				test.reply, conversationSkillToolReplyForTest(valid),
			})
			response := fixture.save(context.Background())
			if response.Code != http.StatusCreated {
				t.Fatalf("corrected save status=%d body=%s", response.Code, response.Body.String())
			}
			requests := fixture.capturedRequests()
			if len(requests) != 2 {
				t.Fatalf("authoring requests=%d want=2", len(requests))
			}
			deadlines := fixture.capturedDeadlines()
			if len(deadlines) != 2 || deadlines[0].IsZero() || !deadlines[0].Equal(deadlines[1]) {
				t.Fatalf("format correction reset its authoring deadline: %v", deadlines)
			}
			for _, request := range requests {
				assertConversationSkillDraftRequest(t, request)
			}
			if _, found := findCatalogSkill(fixture.server.skillCatalog, "typed-fixture-workflow"); !found {
				t.Fatal("corrected draft was not installed")
			}
			if _, err := os.Stat(filepath.Join(fixture.root, "skills", "must-not-install")); !os.IsNotExist(err) {
				t.Fatalf("unvalidated first response was installed: %v", err)
			}
		})
	}
}

func TestConversationSkillAuthoringStopsAfterOneFormatCorrection(t *testing.T) {
	reply := conversationSkillToolReplyForTest(conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) {
		delete(fields, "skill_markdown")
	}))
	fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{reply, reply})
	response := fixture.save(context.Background())
	if response.Code != http.StatusBadGateway || len(fixture.capturedRequests()) != 2 {
		t.Fatalf("exhausted save status=%d requests=%d body=%s", response.Code, len(fixture.capturedRequests()), response.Body.String())
	}
	fixture.assertNoInstallation(t)
}

func TestConversationSkillAuthoringNeverInstallsFreeTextDraft(t *testing.T) {
	reply := conversationSkillAuthoringReply{message: map[string]any{
		"role": "assistant", "content": "```json\n" + conversationSkillDraftArgumentsForTest(t, nil) + "\n```",
	}}
	fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{reply, reply})
	response := fixture.save(context.Background())
	if response.Code != http.StatusBadGateway || len(fixture.capturedRequests()) != 2 {
		t.Fatalf("free-text save status=%d requests=%d body=%s", response.Code, len(fixture.capturedRequests()), response.Body.String())
	}
	fixture.assertNoInstallation(t)
}

func TestConversationSkillAuthoringDoesNotCorrectNonFormatFailures(t *testing.T) {
	cases := []struct {
		name  string
		reply conversationSkillAuthoringReply
	}{
		{"permission", conversationSkillAuthoringReply{status: http.StatusForbidden}},
		{"upstream_failure", conversationSkillAuthoringReply{status: http.StatusServiceUnavailable}},
		{"model_refusal", conversationSkillAuthoringReply{
			stopReason: "content_filter", message: map[string]any{"role": "assistant", "content": "Synthetic provider refusal."},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{test.reply})
			response := fixture.save(context.Background())
			if response.Code != http.StatusBadGateway || len(fixture.capturedRequests()) != 1 {
				t.Fatalf("non-format save status=%d requests=%d body=%s", response.Code, len(fixture.capturedRequests()), response.Body.String())
			}
			fixture.assertNoInstallation(t)
		})
	}
}

func TestConversationSkillAuthoringDoesNotCorrectTransportOrCancellation(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		name := "transport"
		if cancelRequest {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newConversationSkillAuthoringFixture(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var attempts atomic.Int32
			fixture.server.httpClient = &http.Client{Transport: conversationSkillAuthoringTransportFunc(func(request *http.Request) (*http.Response, error) {
				attempts.Add(1)
				if cancelRequest {
					cancel()
					return nil, request.Context().Err()
				}
				return nil, errors.New("synthetic authoring transport failure")
			})}
			response := fixture.save(ctx)
			if response.Code != http.StatusBadGateway || attempts.Load() != 1 {
				t.Fatalf("transport save status=%d attempts=%d body=%s", response.Code, attempts.Load(), response.Body.String())
			}
			fixture.assertNoInstallation(t)
		})
	}
}

type conversationSkillAuthoringReply struct {
	status     int
	message    map[string]any
	stopReason string
}

type conversationSkillAuthoringFixture struct {
	server    *Server
	root      string
	frameID   string
	mu        sync.Mutex
	requests  []map[string]json.RawMessage
	deadlines []time.Time
}

func newConversationSkillAuthoringFixture(t *testing.T, replies []conversationSkillAuthoringReply) *conversationSkillAuthoringFixture {
	t.Helper()
	fixture := &conversationSkillAuthoringFixture{root: t.TempDir(), frameID: "skill-authoring-frame"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected authoring endpoint: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected fixture endpoint", http.StatusBadRequest)
			return
		}
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode fixture request: %v", err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		index := len(fixture.requests)
		fixture.requests = append(fixture.requests, request)
		fixture.mu.Unlock()
		if index >= len(replies) {
			http.Error(w, "unexpected extra authoring request", http.StatusBadRequest)
			return
		}
		reply := replies[index]
		if reply.status >= 400 {
			http.Error(w, "synthetic provider rejection", reply.status)
			return
		}
		reason := reply.stopReason
		if reason == "" {
			reason = "stop"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "fixture-author", "choices": []any{map[string]any{"finish_reason": reason, "message": reply.message}},
		})
	}))
	t.Cleanup(upstream.Close)
	store, repository, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "skill-authoring-project", fixture.frameID)
	stream, err := repository.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "skill-authoring-stream", OwnerID: "local", ExternalID: fixture.frameID, SessionID: fixture.frameID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "skill-authoring-project",
		RootFrameID: fixture.frameID, FrameID: fixture.frameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := repository.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "skill-authoring-user",
		FrameEventID: "skill-authoring-event", MessageUUID: "skill-authoring-message", Text: "Save a reusable input-validation workflow.",
	}); err != nil || !created {
		t.Fatalf("append authoring input created=%t err=%v", created, err)
	}
	enabled := true
	provider, err := store.RegisterModelProvider(workspace.ModelProviderInput{
		ID: "skill-authoring-provider", UserID: "local", Name: "Fixture author", Type: "openai-compatible",
		BaseURL: upstream.URL + "/v1", Model: "fixture-author", Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetFrameRuntimeMetadata(fixture.frameID, workspace.FrameRuntimeMetadata{
		ContextData: map[string]any{"web_assistant": map[string]any{
			"conversation_overrides": map[string]any{"model": "provider:" + provider.ID},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	client := upstream.Client()
	transport := client.Transport
	client.Transport = conversationSkillAuthoringTransportFunc(func(request *http.Request) (*http.Response, error) {
		deadline, _ := request.Context().Deadline()
		fixture.mu.Lock()
		fixture.deadlines = append(fixture.deadlines, deadline)
		fixture.mu.Unlock()
		return transport.RoundTrip(request)
	})
	fixture.server = New(Options{Workspace: store, Transcript: repository, FileRoot: fixture.root, HTTPClient: client})
	return fixture
}

func (fixture *conversationSkillAuthoringFixture) save(ctx context.Context) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"conversation_id": fixture.frameID})
	request := newLoopbackTestRequest(http.MethodPost, "/api/skills/from-conversation", bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", "local")
	response := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(response, request)
	return response
}

func (fixture *conversationSkillAuthoringFixture) capturedRequests() []map[string]json.RawMessage {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]map[string]json.RawMessage(nil), fixture.requests...)
}

func (fixture *conversationSkillAuthoringFixture) capturedDeadlines() []time.Time {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]time.Time(nil), fixture.deadlines...)
}

func (fixture *conversationSkillAuthoringFixture) assertNoInstallation(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fixture.root, "skills"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed authoring installed Skill entries: %v", entries)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, "typed-fixture-workflow"); found {
		t.Fatal("failed authoring published a live catalog entry")
	}
}

func conversationSkillDraftArgumentsForTest(t *testing.T, edit func(map[string]any)) string {
	t.Helper()
	fields := map[string]any{
		"name": "typed-fixture-workflow", "description": "A reusable fixture workflow.",
		"skill_markdown": "# Fixture workflow\n\nPreserve \"quoted input\" and literal backslashes: C:\\fixture\\data.\n\nValidate inputs before using the optional helper.",
		"kernel_py":      "def helper(value):\n    return value + 1\n",
	}
	if edit != nil {
		edit(fields)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func conversationSkillToolCallForTest(id, name, arguments string) map[string]any {
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}
}

func conversationSkillToolReplyForTest(arguments string) conversationSkillAuthoringReply {
	return conversationSkillAuthoringReply{message: map[string]any{
		"role": "assistant", "tool_calls": []any{conversationSkillToolCallForTest("draft-return", conversationSkillReturnToolForTest, arguments)},
	}}
}

func assertConversationSkillDraftRequest(t *testing.T, request map[string]json.RawMessage) {
	t.Helper()
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(request["tools"], &tools); err != nil || len(tools) != 1 {
		t.Fatalf("authoring tools=%s err=%v", request["tools"], err)
	}
	if tools[0].Type != "function" || tools[0].Function.Name != conversationSkillReturnToolForTest {
		t.Fatalf("authoring tool=%#v", tools[0])
	}
	schema := tools[0].Function.Parameters
	properties, ok := schema["properties"].(map[string]any)
	if schema["type"] != "object" || schema["additionalProperties"] != false || !ok || len(properties) != 4 {
		t.Fatalf("authoring schema=%#v", schema)
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 4 {
		t.Fatalf("authoring required fields=%#v", schema["required"])
	}
	for _, name := range []string{"name", "description", "skill_markdown", "kernel_py"} {
		property, ok := properties[name].(map[string]any)
		if !ok || property["type"] != "string" {
			t.Fatalf("authoring field %s=%#v", name, properties[name])
		}
		found := false
		for _, value := range required {
			found = found || value == name
		}
		if !found {
			t.Fatalf("authoring field %s is not required", name)
		}
	}
	var choice any
	if err := json.Unmarshal(request["tool_choice"], &choice); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"type": "function", "function": map[string]any{"name": conversationSkillReturnToolForTest}}
	if !reflect.DeepEqual(choice, want) {
		t.Fatalf("authoring tool_choice=%#v want=%#v", choice, want)
	}
}

type conversationSkillAuthoringTransportFunc func(*http.Request) (*http.Response, error)

func (transport conversationSkillAuthoringTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
