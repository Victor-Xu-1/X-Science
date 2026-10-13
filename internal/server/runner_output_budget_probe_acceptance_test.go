package server

import (
	"context"
	"path/filepath"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

type probeAcceptanceModel struct {
	response agentruntime.ModelResponse
	cancel   context.CancelFunc
}

func (m probeAcceptanceModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	if m.cancel != nil {
		m.cancel()
	}
	return m.response, nil
}

func (m probeAcceptanceModel) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	return m.Complete(ctx, request)
}

func TestOutputBudgetProbeDoesNotAcceptCancelledOrEmptySuccess(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		cancel  bool
	}{
		{name: "cancelled successful transport", content: "ready", cancel: true},
		{name: "empty successful transport"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			model := probeAcceptanceModel{response: agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: test.content}}}
			if test.cancel {
				model.cancel = cancel
			}
			store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
			defer store.Close()
			limit := 17
			profile := providers.ModelProfile{Provider: providers.ProviderProfile{ID: "acceptance", UserID: "owner"}, MaxTokens: &limit}
			client := newSessionOutputBudgetClient(model, store, profile, "session", "agent").(*sessionOutputBudgetClient)
			request := agentruntime.ModelRequest{}
			contextID, err := outputBudgetContext(request)
			if err != nil {
				t.Fatal(err)
			}
			if err := client.observeGeneration(contextID, providers.OutputLimitDetails{RequestedTokens: 17, OutputTokens: 17}, 0, 0, true); err != nil {
				t.Fatal(err)
			}
			_, _ = client.CompleteStream(ctx, request, nil)
			state, err := client.read()
			if err != nil || state.Next != 34 || !state.DefaultProbeUsed || state.DefaultProbePending {
				t.Fatalf("unaccepted result became a successful default preference: state=%+v err=%v", state, err)
			}
		})
	}
}
