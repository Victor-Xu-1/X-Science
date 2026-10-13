package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

func TestOutputBudgetSuccessfulDefaultProbePreferenceSurvivesNewTaskAndRestart(t *testing.T) {
	f := newOutputProbeFixture(t, providers.ProtocolOpenAICompatible, func(w http.ResponseWriter, budget int) {
		if budget == 17 {
			writeProbeLimit(w, providers.ProtocolOpenAICompatible, "private", 17)
		} else {
			writeProbeSuccess(w, providers.ProtocolOpenAICompatible)
		}
	})
	if _, err := f.call(t, agentruntime.ModelRequest{}); !providers.IsProviderOutputTokenLimit(err) {
		t.Fatalf("initial truncation lost: %v", err)
	}
	// A failed allowance alone is not enough to teach another task a default.
	if _, err := callOutputPreferenceTask(t, f, "before-success", agentruntime.ModelRequest{}); !providers.IsProviderOutputTokenLimit(err) {
		t.Fatalf("unconfirmed preference escaped task: %v", err)
	}
	if _, err := f.call(t, agentruntime.ModelRequest{}); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	if response, err := callOutputPreferenceTask(t, f, "new-task", agentruntime.ModelRequest{}); err != nil || response.Message.Content != "ready" {
		t.Fatalf("new task response=%+v err=%v", response, err)
	}
	if _, err := callOutputPreferenceTask(t, f, "hard-budget-task", agentruntime.ModelRequest{MaxTokens: 7}); err != nil {
		t.Fatal(err)
	}
	if got := f.budgets(5); !reflect.DeepEqual(got, []int{17, 17, 0, 0, 7}) {
		t.Fatalf("new task repeated disproven seed or ignored hard cap: %v", got)
	}
}

func callOutputPreferenceTask(t *testing.T, f *outputProbeFixture, session string, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	t.Helper()
	delegate, err := providers.NewRuntimeModelClient(f.profile, f.api.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := newSessionOutputBudgetClient(delegate, f.store, f.profile, session, "agent").(agentruntime.StreamingModelClient)
	return client.CompleteStream(context.Background(), request, nil)
}

func TestOutputBudgetPreferenceIsConfigurationScopedAndCopiesNoTaskBounds(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	defer store.Close()
	profile := outputPreferenceTestProfile()
	client := newSessionOutputBudgetClient(nil, store, profile, "learned", "agent").(*sessionOutputBudgetClient)
	older := newSessionOutputBudgetClient(nil, store, profile, "already-started", "agent").(*sessionOutputBudgetClient)
	if state, err := older.initializeOutputBudgetPreference(); err != nil || state.SkipInitialAllowance {
		t.Fatalf("unexpected initial choice=%+v err=%v", state, err)
	}
	if err := client.rememberOutputBudgetPreference(context.Background(), acceptedPreferenceProbe(), validPreferenceResponse()); err != nil {
		t.Fatal(err)
	}
	if state, err := older.initializeOutputBudgetPreference(); err != nil || state.SkipInitialAllowance {
		t.Fatalf("peer learning changed an active task: %+v %v", state, err)
	}
	for _, change := range []string{"same", "owner", "provider", "provider-type", "endpoint", "protocol", "model", "role", "allowance", "temperature", "missing-owner"} {
		t.Run(change, func(t *testing.T) {
			p, role := profile, "agent"
			switch change {
			case "owner":
				p.Provider.UserID = "another-owner"
			case "provider":
				p.Provider.ID = "another-provider"
			case "provider-type":
				p.Provider.Type = "another-type"
			case "endpoint":
				p.Provider.Endpoint += "/other"
			case "protocol":
				p.Provider.Protocol = providers.ProtocolAnthropic
			case "model":
				p.Model = "another-model"
			case "role":
				role = "reviewer"
			case "allowance":
				value := 18
				p.MaxTokens = &value
			case "temperature":
				value := 0.2
				p.Temperature = &value
			case "missing-owner":
				p.Provider.UserID = ""
			}
			other := newSessionOutputBudgetClient(nil, store, p, "new-"+change, role).(*sessionOutputBudgetClient)
			state, err := other.initializeOutputBudgetPreference()
			if err != nil || state.SkipInitialAllowance != (change == "same") || state.Next != 0 || state.Lower != 0 || state.Ceiling != 0 || state.Rejected != 0 || state.Context != "" {
				t.Fatalf("preference scope or copied task facts=%+v err=%v", state, err)
			}
		})
	}
}

func TestOutputBudgetPreferenceRequiresSuccessfulCompleteUncancelledInitialProbe(t *testing.T) {
	for _, name := range []string{"empty", "private-only", "error", "cancel", "partial-tool", "content-and-partial-tool", "protocol-diagnostic", "noninitial-probe"} {
		t.Run(name, func(t *testing.T) {
			store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
			defer store.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			model := preferenceOutcomeModel{response: validPreferenceResponse()}
			requested := 17
			switch name {
			case "empty":
				model.response = agentruntime.ModelResponse{}
			case "private-only":
				model.response.Message = agentruntime.Message{Role: "assistant", ReasoningContent: "private fixture"}
			case "error":
				model.err = errors.New("provider interrupted")
			case "cancel":
				model.cancel = cancel
			case "partial-tool", "content-and-partial-tool", "protocol-diagnostic":
				call := agentruntime.ToolCall{ID: "c", Name: "read_file", Arguments: json.RawMessage(`{`)}
				if name == "protocol-diagnostic" {
					call.Arguments = json.RawMessage(`{}`)
					call.ProviderProtocolDiagnostic = "incomplete original arguments"
				}
				model.response.Message.ToolCalls = []agentruntime.ToolCall{call}
				if name != "content-and-partial-tool" {
					model.response.Message.Content = ""
				}
			case "noninitial-probe":
				requested = 34
			}
			client := newSessionOutputBudgetClient(model, store, outputPreferenceTestProfile(), "task", "agent").(*sessionOutputBudgetClient)
			identity, err := outputBudgetContext(agentruntime.ModelRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.observeGeneration(identity, providers.OutputLimitDetails{RequestedTokens: requested, OutputTokens: requested}, 0, 0, true); err != nil {
				t.Fatal(err)
			}
			_, _ = client.CompleteStream(ctx, agentruntime.ModelRequest{}, nil)
			if _, found, err := store.Get(client.preferenceNamespace, "preference"); err != nil || found {
				t.Fatalf("invalid comparison learned a cross-task preference: found=%t err=%v", found, err)
			}
		})
	}
}

func TestOutputBudgetPreferenceConcurrentLearningIsIdempotentAndMinimal(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	defer store.Close()
	profile := outputPreferenceTestProfile()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newSessionOutputBudgetClient(nil, store, profile, fmt.Sprintf("task-%d", i), "agent").(*sessionOutputBudgetClient)
			results <- client.rememberOutputBudgetPreference(context.Background(), acceptedPreferenceProbe(), validPreferenceResponse())
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	client := newSessionOutputBudgetClient(nil, store, profile, "inspect", "agent").(*sessionOutputBudgetClient)
	entry, found, err := store.Get(client.preferenceNamespace, "preference")
	if err != nil || !found || entry.Version != 1 {
		t.Fatalf("non-idempotent preference: %+v found=%t err=%v", entry, found, err)
	}
	raw, err := json.Marshal(entry.Value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields["version"] != float64(1) || fields["ownerUserId"] != profile.Provider.UserID || fields["scope"] != client.preferenceNamespace || fields["preferProviderDefault"] != true || strings.Contains(string(raw), profile.Provider.Endpoint) {
		t.Fatalf("preference contains more than its permitted metadata: %s", raw)
	}
}

func outputPreferenceTestProfile() providers.ModelProfile {
	allowance := 17
	return providers.ModelProfile{Provider: providers.ProviderProfile{ID: "provider", UserID: "owner", Endpoint: "https://provider.example/v1", Protocol: providers.ProtocolOpenAICompatible}, Model: "model", MaxTokens: &allowance}
}

func acceptedPreferenceProbe() outputBudgetState {
	return outputBudgetState{DefaultProbeUsed: true, DefaultProbeFromInitial: true}
}

func validPreferenceResponse() agentruntime.ModelResponse {
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "ready"}, StopReason: "stop"}
}

type preferenceOutcomeModel struct {
	response agentruntime.ModelResponse
	err      error
	cancel   context.CancelFunc
}

func (model preferenceOutcomeModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	if model.cancel != nil {
		model.cancel()
	}
	return model.response, model.err
}

func (model preferenceOutcomeModel) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	return model.Complete(ctx, request)
}
