package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestCompletedReviewCancelsAndJoinsUnsubmittedBookmarker(t *testing.T) {
	const candidate = "The requested result is ready."
	bookmarkerStarted := make(chan struct{})
	bookmarkerCancelled := make(chan struct{})
	release := make(chan struct{})
	var startOnce, cancelOnce sync.Once
	modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		hasSubmit, hasRepl := false, false
		for _, tool := range request.Tools {
			hasSubmit = hasSubmit || tool.Function.Name == sessionReviewerSubmitToolName
			hasRepl = hasRepl || tool.Function.Name == "repl"
		}
		message := map[string]any{"role": "assistant", "content": candidate}
		if hasSubmit && !hasRepl {
			startOnce.Do(func() { close(bookmarkerStarted) })
			select {
			case <-r.Context().Done():
				cancelOnce.Do(func() { close(bookmarkerCancelled) })
			case <-release:
			}
			return
		}
		if hasSubmit {
			select {
			case <-bookmarkerStarted:
			case <-r.Context().Done():
				return
			}
			message = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
				"id": "review-submit", "type": "function", "function": map[string]any{
					"name":      sessionReviewerSubmitToolName,
					"arguments": `{"human_description":"Reviewing completed result","findings":[]}`,
				},
			}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "bookmarker-lifetime-response", "model": "test-model",
			"choices": []any{map[string]any{"message": message, "finish_reason": "stop"}},
		})
	}))
	t.Cleanup(func() { close(release); modelAPI.Close() })
	app, store, options := newBookmarkerLifetimeFixture(t, modelAPI.URL)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	type outcome struct {
		status string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		cycle, err := app.RunSessionRunnerChatOnce(ctx, options)
		done <- outcome{status: cycle.Status, err: err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		frames, err := store.ListFramesForRoot("root")
		if err != nil {
			t.Fatal(err)
		}
		settled := false
		for _, frame := range frames {
			settled = settled || frame.AgentName == "REVIEWER" && frame.Status == "completed"
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not complete primary review: frames=%#v", frames)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case result := <-done:
		// Keep cleanup nonblocking after consuming the one result.
		done <- result
		if result.err != nil || result.status != "completed" {
			t.Fatalf("ready candidate failed: status=%s err=%v", result.status, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("completed primary/reviewer is held by the optional bookmarker request")
	}
	select {
	case <-bookmarkerCancelled:
	case <-time.After(time.Second):
		t.Fatal("owned bookmarker HTTP request was not cancelled and joined")
	}
	frames, err := store.ListFramesForRoot("root")
	if err != nil {
		t.Fatal(err)
	}
	bookmarkers := 0
	for _, frame := range frames {
		if frame.AgentName == "BOOKMARKER" {
			bookmarkers++
			if frame.Status != "cancelled" {
				t.Fatalf("unsubmitted bookmarker was not honestly settled: %#v", frame)
			}
			runtime, found, err := app.transcriptStore.GetLatestRunnerRuntimeState(context.Background(), "frame:"+frame.ID, "owner")
			if err != nil || !found || runtime.Status != "cancelled" {
				t.Fatalf("bookmarker claim remained active: runtime=%#v found=%t err=%v", runtime, found, err)
			}
		}
	}
	annotations, err := store.ListTranscriptAnnotations("root")
	if err != nil || len(annotations) != 0 || bookmarkers != 1 {
		t.Fatalf("unexpected auxiliary outcome: annotations=%#v frames=%d err=%v", annotations, bookmarkers, err)
	}
	stream, found, err := app.transcriptStore.GetFrameStreamBySession(context.Background(), "owner", "root")
	if err != nil || !found {
		t.Fatalf("canonical task stream found=%t err=%v", found, err)
	}
	events, err := app.transcriptStore.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	finals := 0
	for _, event := range events {
		if event.Event.Type == "assistant_message" {
			finals++
			if text := transcriptPayloadText(mustTranscriptPayloadObject(t, event.ResolvedPayloadJSON)); text != candidate {
				t.Fatalf("canonical final=%q, want %q", text, candidate)
			}
		}
	}
	if finals != 1 {
		t.Fatalf("canonical final publication count=%d events=%#v", finals, events)
	}
}

func newBookmarkerLifetimeFixture(t *testing.T, endpoint string) (*Server, *workspace.Store, SessionRunnerChatOptions) {
	t.Helper()
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: "root", ProjectID: "project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	repository, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	app := New(Options{FileRoot: root, Workspace: store, Transcript: repository})
	if err := app.sessionStore.Upsert(sessionstore.Session{
		ID: "root", Title: "Reviewed task", WorkDir: root,
		Project:       &sessionstore.Project{ID: "project", Name: "Project", Path: root, BoundAt: time.Now().UTC()},
		Orchestration: map[string]any{"sessionConfig": map[string]any{"verifier_mode": "off"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "root", MessageUUID: "user-message", ClientMessageID: "user-client", Text: "Return the validated result.",
		RuntimeConfig: map[string]any{"verifier_mode": "on"},
	}); err != nil {
		t.Fatal(err)
	}
	seedAnsweredTaskIntake(t, app, "owner", "root")
	return app, store, SessionRunnerChatOptions{
		SessionID: "root", RunnerID: "bookmarker-lifetime-runner", Endpoint: endpoint,
		Model: "test-model", RequestTimeout: time.Minute, MaxAttempts: 1, LeaseTTL: time.Minute,
		ReplayLimit: 100, OutputLimitBytes: 64 << 10, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	}
}
