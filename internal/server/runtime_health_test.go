package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestRuntimeComponentHealthPublishesFailureState(t *testing.T) {
	server := New(Options{SkillDirectories: []string{v11SkillsDir(t)}})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	server.ReportRuntimeComponent("task-dispatch", errors.New("provider secret must not be retained"))
	response := httptest.NewRecorder()
	server.handleHealth(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want503", response.Code)
	}
	var health struct {
		Components []string          `json:"degraded_components"`
		States     map[string]string `json:"runtime_component_states"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(health.Components, []string{"task-dispatch"}) || !reflect.DeepEqual(health.States, map[string]string{"task-dispatch": "failed"}) {
		t.Fatalf("health component identities and states disagree: %#v", health)
	}
	if strings.Contains(response.Body.String(), "provider secret") {
		t.Fatal("health leaked the component error text")
	}
}

func TestRuntimeComponentHealthIsBoundedSortedAndRecoverable(t *testing.T) {
	server := &Server{}
	server.ReportRuntimeComponent("runner-chat", errors.New("provider secret must not be retained"))
	server.ReportRuntimeComponent("compute-poller", errors.New("transient"))
	server.ReportRuntimeComponent("", errors.New("ignored"))
	server.ReportRuntimeComponent(string(make([]byte, 81)), errors.New("ignored"))
	if got, _ := server.runtimeComponentHealth(); !reflect.DeepEqual(got, []string{"compute-poller", "runner-chat"}) {
		t.Fatalf("degraded=%#v", got)
	}
	server.ReportRuntimeComponent("runner-chat", nil)
	if got, _ := server.runtimeComponentHealth(); !reflect.DeepEqual(got, []string{"compute-poller"}) {
		t.Fatalf("recovered=%#v", got)
	}
}

func TestRuntimeComponentHealthKeepsFailureUntilExplicitReadiness(t *testing.T) {
	server := New(Options{SkillDirectories: []string{v11SkillsDir(t)}})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	steps := []struct {
		name  string
		err   error
		state string
	}{
		{"initial", ErrRuntimeComponentStarting, "starting"},
		{"failed", errors.New("private failure detail"), "failed"},
		{"retry", errors.Join(errors.New("retry"), ErrRuntimeComponentStarting), "failed"},
		{"ready", nil, ""},
		{"later-start", ErrRuntimeComponentStarting, "starting"},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			server.ReportRuntimeComponent("ordinary-component", step.err)
			response := httptest.NewRecorder()
			server.handleHealth(response, httptest.NewRequest(http.MethodGet, "/health", nil))
			var health struct {
				States map[string]string `json:"runtime_component_states"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
				t.Fatal(err)
			}
			if got := health.States["ordinary-component"]; got != step.state {
				t.Fatalf("state=%q want=%q", got, step.state)
			}
			wantCode := http.StatusServiceUnavailable
			if step.state == "" {
				wantCode = http.StatusOK
			}
			if response.Code != wantCode {
				t.Fatalf("status=%d want=%d", response.Code, wantCode)
			}
		})
	}
}

func TestRuntimeComponentHealthSnapshotIsAtomicAndIndependent(t *testing.T) {
	server := &Server{}
	var workers sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				server.ReportRuntimeComponent(name, ErrRuntimeComponentStarting)
				server.ReportRuntimeComponent(name, errors.New("private"))
				server.ReportRuntimeComponent(name, nil)
			}
		}()
	}
	for range 100 {
		components, states := server.runtimeComponentHealth()
		if len(components) != len(states) || !sort.StringsAreSorted(components) {
			t.Errorf("contradictory snapshot: %#v %#v", components, states)
		}
		for _, name := range components {
			if states[name] != runtimeComponentStarting && states[name] != runtimeComponentFailed {
				t.Errorf("invalid state for %s: %q", name, states[name])
			}
		}
		states["foreign-copy-only"] = runtimeComponentFailed
	}
	workers.Wait()
	components, states := server.runtimeComponentHealth()
	if len(components) != 0 || len(states) != 0 {
		t.Fatalf("returned snapshot mutated the owner: %#v %#v", components, states)
	}
}

func TestScientificCoreRuntimeHealthSeparatesGatewayFromRuntimeReadiness(t *testing.T) {
	server := &Server{kernelManager: kernelruntime.NewManager(kernelruntime.Config{})}
	health := server.scientificCoreRuntimeHealth()
	if health["ready"] != false || health["required"] != true || health["platform"] == "" {
		t.Fatalf("scientific core health=%#v", health)
	}
	if health["status"] != "unsupported" && health["status"] != "unavailable" {
		t.Fatalf("scientific core status=%#v", health)
	}
}
