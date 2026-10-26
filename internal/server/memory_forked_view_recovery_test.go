package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorypolicy"
	"x-science/internal/outbox"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

func TestMemoryForkedOversizeRecoveryDispatchesCapturedCompactDelta(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	// Existing JSON entry point compiles on the original source. The old decoder
	// ignores the new field, so the first assertion exposes actual oversize loss.
	if err := json.Unmarshal([]byte(`{"enabled":true,"extract_enabled":true,"extract_mode":"forked","extract_every_n":1,"pi_classifier_enabled":false,"forked_view_recovery":"compact-on-oversize"}`), &f.server.memoryConfig); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || r.URL.Path != "/v1/messages" || payload["model"] != memoryExtractionTestModel {
			t.Errorf("captured provider request identity mismatch: path=%s err=%v", r.URL.Path, err)
			http.Error(w, "unexpected synthetic provider request", http.StatusBadRequest)
			return
		}
		calls.Add(1)
		choice, _ := payload["tool_choice"].(map[string]any)
		if choice["type"] != "tool" || choice["name"] != memoryextract.EmitToolName {
			t.Error("recovery did not use the original compact acknowledgement contract")
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 1 {
			t.Error("compact recovery did not retain its one-message bounded input")
		} else {
			message, _ := messages[0].(map[string]any)
			parts, _ := message["content"].([]any)
			if len(parts) != 2 {
				t.Error("compact recovery lost its transcript/prompt parts")
			} else {
				text := strings.TrimSuffix(strings.TrimPrefix(memoryTextBlock(parts[0]), "<transcript>\n"), "\n</transcript>")
				if memorypolicy.UTF16Length(text) > memorypolicy.ExtractionTranscriptMaxUTF16Units || !strings.Contains(text, "Create scientific artifacts.") || strings.Contains(text, "OVERSIZED_TRANSIENT_SYSTEM_EVIDENCE") {
					t.Error("recovery replaced the source-selected bounded delta with expanded model context")
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "memory_recovery_fixture", "role": "assistant", "model": memoryExtractionTestModel, "stop_reason": "tool_use",
			"content": []any{map[string]any{"type": "tool_use", "id": "emit_recovery", "name": memoryextract.EmitToolName,
				"input": map[string]any{"append": []any{map[string]any{"text": "Preserve immutable source hashes in reproducible reports.", "evidence": "stated", "entity": "project:project-save"}}}}},
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 4},
		})
	}))
	defer provider.Close()
	enabled := true
	const providerID = "memory-recovery-original"
	if _, err := f.store.RegisterModelProvider(workspace.ModelProviderInput{
		ID: providerID, UserID: f.stream.OwnerID, Name: "Memory recovery fixture", Type: "anthropic", BaseURL: provider.URL + "/v1", Model: memoryExtractionTestModel, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	f.server.httpClient = provider.Client()
	profile, err := providers.ResolveUserModelProfile(f.server.settingsStore, f.store, f.server.secretStore, f.stream.OwnerID, memoryExtractionTestModel,
		providers.ResolutionInput{Context: t.Context(), ProjectID: f.stream.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	model := workspace.MemoryExtractionModelIdentity{Model: profile.Model, ProviderID: profile.Provider.ID,
		ProfileSHA256: memoryViewDigest([]byte(strings.Join([]string{strings.TrimSpace(profile.Provider.ID), strings.TrimSpace(profile.Provider.Endpoint), strings.TrimSpace(profile.Model)}, "\x00")))}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "system", Content: strings.Repeat("OVERSIZED_TRANSIENT_SYSTEM_EVIDENCE ", 28000)}, {Role: "user", Content: "Bounded current user request."}}}
	captured := f.server.memoryExtraction.recordLastRequest(f.stream.SessionID, f.claim.Attempt, request)
	f.server.memoryExtraction.bindRecordedModel(f.stream.SessionID, f.claim.Attempt, captured, model)
	f.server.memoryExtraction.tasks = nil
	result, err := settleDurableMemoryFixture(t, f, "Preserve immutable source hashes in reproducible reports.", nil)
	if err != nil || result.Status != "completed" {
		t.Fatalf("settlement failed: status=%q err=%v", result.Status, err)
	}
	event := claimDurableMemoryFixture(t, f.store, "oversize-recovery-fixture")
	value, err := workspace.DecodeMemoryExtractionDispatch(event)
	if err != nil {
		t.Fatal(err)
	}
	if value.Completion.Mode != "compact" || value.Completion.Diagnostic != "" {
		t.Fatalf("valid oversized captured request was not assigned explicit bounded recovery: mode=%q diagnostic=%q", value.Completion.Mode, value.Completion.Diagnostic)
	}
	if len(value.Completion.RequestView) != 0 || value.Completion.RequestViewSHA256 != "" || value.Completion.Model != model {
		t.Fatal("recovery persisted a partial original view or substituted the captured model")
	}
	var dispatch map[string]any
	if err := json.Unmarshal(event.Payload, &dispatch); err != nil {
		t.Fatal(err)
	}
	completion, _ := dispatch["completion"].(map[string]any)
	decision, _ := completion["mode_decision"].(map[string]any)
	if decision["requested_mode"] != "forked" || decision["effective_mode"] != "compact" || decision["reason"] != "forked-view-oversize" || decision["recovery_policy"] != "compact-on-oversize" || decision["request_attempt"] != float64(f.claim.Attempt) || decision["model_profile_sha256"] != model.ProfileSHA256 {
		t.Fatalf("explicit recovery decision missing or unbound: keys=%d", len(decision))
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("delivery=%v", err)
	}
	saved, err := f.store.GetOutboxEvent(t.Context(), event.ID)
	if err != nil || saved.Status != workspace.OutboxStatusDelivered {
		t.Fatalf("delivery status=%q err=%v", saved.Status, err)
	}
	receipt, found, err := f.store.ReadMemoryExtractionReceipt(t.Context(), value.Source)
	if err != nil || !found || !receipt.CursorAdvanced || len(receipt.Applied.Appended) != 1 || calls.Load() != 1 {
		t.Fatalf("source-bound recovery outcome: found=%t cursor=%t appends=%d calls=%d err=%v", found, receipt.CursorAdvanced, len(receipt.Applied.Appended), calls.Load(), err)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var outcome map[string]any
	if err := json.Unmarshal(encoded, &outcome); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome["ModeDecision"], decision) {
		t.Fatal("original receipt readback lost the immutable decision")
	}
	if cursor, err := f.store.LastExtractMessageIndex(t.Context(), f.stream.FrameID); err != nil || cursor != receipt.CursorAfter {
		t.Fatalf("cursor/receipt mismatch: count=%d err=%v", cursor, err)
	}
}
