package memoryextract

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memorypolicy"
	"x-science/internal/providers"
)

func literalRepairProviderMaximum(t *testing.T, maximum int) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
			"param": "max_tokens", "message": "expected a value <= " + strconv.Itoa(maximum),
		}})
	}))
	t.Cleanup(server.Close)
	client, err := providers.NewRuntimeModelClient(providers.ModelProfile{
		Provider: providers.ProviderProfile{ID: "synthetic-cap", Protocol: providers.ProtocolOpenAICompatible, Endpoint: server.URL},
		Model:    "synthetic-model", Request: providers.RequestProfile{MaxAttempts: 1},
	}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "synthetic cap request"}}, MaxTokens: memorypolicy.LiteralRepairMaxTokens,
	})
	if actual, ok := providers.ProviderOutputTokenMaximum(err); !ok || actual != maximum {
		t.Fatalf("provider cap=%d known=%t err=%v", actual, ok, err)
	}
	return err
}

func TestWorkspaceLiteralRepairerRespectsDeclaredProviderMaximum(t *testing.T) {
	provider, _ := literalRepairProvider(t, repairOutputReply{repairFullText, "length"})
	_, limited := provider.RepairMemoryText(context.Background(), RepairRequest{Prompt: "synthetic limit request", MaxTokens: memorypolicy.LiteralRepairMaxTokens})
	if !providers.IsProviderOutputTokenLimit(limited) {
		t.Fatalf("expected a real parsed provider output limit, got %v", limited)
	}
	for _, maximum := range []int{memorypolicy.ExtractionMaxTokens, memorypolicy.LiteralRepairMaxTokens, memorypolicy.LiteralRepairMaxTokens / 2} {
		t.Run(strconv.Itoa(maximum), func(t *testing.T) {
			failure := errors.Join(limited, literalRepairProviderMaximum(t, maximum))
			var requests []RepairRequest
			model := repairModelFunc(func(_ context.Context, request RepairRequest) (string, error) {
				requests = append(requests, request)
				if len(requests) == 1 {
					return repairFullText, failure
				}
				return repairFullText, nil
			})
			result, err := NewModelLiteralRepairer(model).RepairMemoryReplacement(context.Background(), repairOldText, repairUpdatedText)
			if maximum > memorypolicy.LiteralRepairMaxTokens {
				if err != nil || result != repairFullText || len(requests) != 2 || requests[1].MaxTokens != maximum {
					t.Fatalf("result=%q requests=%#v err=%v", result, requests, err)
				}
			} else if result != "" || len(requests) != 1 || !errors.Is(err, failure) {
				t.Fatalf("exhausted hard cap result=%q requests=%#v err=%v", result, requests, err)
			}
		})
	}
}
