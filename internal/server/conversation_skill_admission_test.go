package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestConversationSkillAuthoringBlocksIdleReload(t *testing.T) {
	fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{
		conversationSkillToolReplyForTest(conversationSkillDraftArgumentsForTest(t, nil)),
	})
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	original := fixture.server.httpClient
	fixture.server.httpClient = &http.Client{Transport: conversationSkillAuthoringTransportFunc(func(request *http.Request) (*http.Response, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			return original.Do(request)
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})}
	ctx, cancel := context.WithCancel(context.Background())
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response <- fixture.save(ctx)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("synthetic authoring request did not settle")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic authoring request was not admitted")
	}
	counts := conversationSkillHealthForTest(t, fixture.server)
	if counts["active_session_runs"] != float64(1) || counts["active_runner_sessions"] != float64(0) {
		t.Errorf("non-chat authoring must be active without becoming a fake chat run: %#v", counts)
	}
	drained := fixture.server.TryDrainIdle()
	if drained {
		t.Error("idle reload admitted while the real Save handler still owns model-backed work")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case saved := <-response:
		if saved.Code != http.StatusCreated {
			t.Fatalf("admitted authoring did not finish normally: %d", saved.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("released synthetic authoring request did not finish")
	}
	if counts := conversationSkillHealthForTest(t, fixture.server); counts["active_session_runs"] != float64(0) {
		t.Fatal("finished authoring retained runtime ownership")
	}
	if !drained {
		if !fixture.server.TryDrainIdle() {
			t.Fatal("settled Save still blocks idle reload")
		}
		if rejected := fixture.save(context.Background()); rejected.Code != http.StatusServiceUnavailable {
			t.Fatalf("new Save crossed the drain fence: status=%d", rejected.Code)
		}
		if len(fixture.capturedRequests()) != 1 {
			t.Fatal("a drain-rejected Save called the provider")
		}
	}
}

func conversationSkillHealthForTest(t *testing.T, server *Server) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newLoopbackTestRequest(http.MethodGet, "/health", nil))
	var health map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"active_session_runs": health["active_session_runs"], "active_runner_sessions": health["active_runner_sessions"],
		"active_runtime_operations": health["active_runtime_operations"]}
}

func TestConversationSkillAuthoringCancellationReleasesRuntimeOwnership(t *testing.T) {
	fixture := newConversationSkillAuthoringFixture(t, nil)
	started, finished := make(chan struct{}), make(chan struct{})
	fixture.server.httpClient = &http.Client{Transport: conversationSkillAuthoringTransportFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- fixture.save(ctx); close(finished) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("cancelled synthetic authoring did not settle")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic authoring did not start")
	}
	cancel()
	select {
	case saved := <-response:
		if saved.Code != http.StatusBadGateway {
			t.Fatalf("cancelled authoring status=%d", saved.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled authoring did not return")
	}
	fixture.assertNoInstallation(t)
	counts := conversationSkillHealthForTest(t, fixture.server)
	if counts["active_session_runs"] != float64(0) || counts["active_runtime_operations"] != float64(0) || !fixture.server.TryDrainIdle() {
		t.Fatal("cancelled authoring retained operation ownership")
	}
}
