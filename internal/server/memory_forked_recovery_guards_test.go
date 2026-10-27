package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	"x-science/internal/outbox"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

func rememberOversizedRecoveryFixture(t *testing.T, f *agentSaveArtifactsFixture) (agentruntime.ModelRequest, workspace.MemoryExtractionModelIdentity) {
	t.Helper()
	enabled := true
	if _, err := f.store.RegisterModelProvider(workspace.ModelProviderInput{ID: "recovery-guard-provider", UserID: f.stream.OwnerID, Name: "Synthetic recovery guard", Type: "anthropic", BaseURL: "http://127.0.0.1:1/v1", Model: memoryExtractionTestModel, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	profile, err := providers.ResolveUserModelProfile(f.server.settingsStore, f.store, f.server.secretStore, f.stream.OwnerID, memoryExtractionTestModel, providers.ResolutionInput{Context: t.Context(), ProjectID: f.stream.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	model := workspace.MemoryExtractionModelIdentity{Model: profile.Model, ProviderID: profile.Provider.ID, ProfileSHA256: memoryViewDigest([]byte(strings.Join([]string{strings.TrimSpace(profile.Provider.ID), strings.TrimSpace(profile.Provider.Endpoint), strings.TrimSpace(profile.Model)}, "\x00")))}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "system", Content: strings.Repeat("SYNTHETIC_OVERSIZE_CONTEXT ", 33000)}}}
	bindRecoveryFixture(f, request, model, f.claim.Attempt)
	return request, model
}

func bindRecoveryFixture(f *agentSaveArtifactsFixture, request agentruntime.ModelRequest, model workspace.MemoryExtractionModelIdentity, attempt int64) {
	captured := f.server.memoryExtraction.recordLastRequest(f.stream.SessionID, attempt, request)
	f.server.memoryExtraction.bindRecordedModel(f.stream.SessionID, attempt, captured, model)
}

type misleadingOversizeJSON struct{}

func (misleadingOversizeJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("provider diagnostic says oversize")
}

func TestMemoryForkedOversizeRecoveryRequiresTypedOriginalAuthority(t *testing.T) {
	for _, name := range []string{"explicit-strict", "missing-request", "missing-model-profile", "foreign-claim", "changed-provider-profile", "unsupported-json", "diagnostic-text-is-not-proof"} {
		t.Run(name, func(t *testing.T) {
			f := prepareDurableMemoryFixture(t)
			f.server.memoryConfig.ForkedViewRecovery = memoryconfig.ForkedViewRecoveryCompact
			request, model := rememberOversizedRecoveryFixture(t, f)
			authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
			diagnostic := "original-request-view-oversize"
			switch name {
			case "explicit-strict":
				f.server.memoryConfig.ForkedViewRecovery = memoryconfig.ForkedViewRecoveryStrict
			case "missing-request":
				f.server.memoryExtraction.discardAttemptRequest(f.stream.SessionID, f.claim.Attempt)
				diagnostic = "original-request-view-missing"
			case "missing-model-profile":
				model.ProfileSHA256 = ""
				bindRecoveryFixture(f, request, model, f.claim.Attempt)
			case "foreign-claim":
				authority.Claim.OwnerID = "foreign-owner"
			case "changed-provider-profile":
				enabled := true
				if _, err := f.store.UpsertModelProvider(workspace.ModelProviderInput{ID: model.ProviderID, UserID: f.stream.OwnerID, Name: "Changed synthetic provider", Type: "anthropic", BaseURL: "http://127.0.0.1:2/v1", Model: model.Model, Enabled: &enabled}); err != nil {
					t.Fatal(err)
				}
			case "unsupported-json":
				request.ToolChoice = make(chan struct{})
				bindRecoveryFixture(f, request, model, f.claim.Attempt)
				diagnostic = "original-request-view-unsupported-json"
			case "diagnostic-text-is-not-proof":
				request.Messages = nil
				request.ToolChoice = misleadingOversizeJSON{}
				bindRecoveryFixture(f, request, model, f.claim.Attempt)
				diagnostic = "original-request-view-unsupported-json"
			}
			completion, err := f.server.memoryExtraction.prepareCompletedMemoryExtraction(t.Context(), authority)
			if err != nil || completion == nil || completion.Mode != "forked" || completion.Diagnostic != diagnostic || completion.ModeDecision.Recovered() || len(completion.RequestView) != 0 {
				t.Fatalf("invalid recovery authority accepted: completion=%+v err=%v", completion, err)
			}
			requireNoDurableMemoryEffects(t, f)
		})
	}
}

func TestMemoryForkedRecoveryPolicyChangesRevokeUncommittedEffects(t *testing.T) {
	for _, name := range []string{"strict-before-model", "disabled-before-model", "strict-after-model", "strict-before-commit"} {
		t.Run(name, func(t *testing.T) {
			f := prepareDurableMemoryFixture(t)
			f.server.memoryConfig.ForkedViewRecovery = memoryconfig.ForkedViewRecoveryCompact
			rememberOversizedRecoveryFixture(t, f)
			f.server.memoryExtraction.tasks = nil
			result, err := settleDurableMemoryFixture(t, f, "Retain explicit random seeds in reproducible reports.", nil)
			if err != nil || result.Status != "completed" {
				t.Fatalf("settlement=%+v err=%v", result, err)
			}
			event := claimDurableMemoryFixture(t, f.store, "recovery-policy-guard")
			decoded, err := workspace.DecodeMemoryExtractionDispatch(event)
			if err != nil || !decoded.Completion.ModeDecision.Recovered() {
				t.Fatalf("fixture lacks recovery decision: %v", err)
			}
			revoke := func() {
				if strings.HasPrefix(name, "disabled") {
					if err := f.server.setMemoryAutoExtractionEnabled(f.stream.OwnerID, false); err != nil {
						t.Fatal(err)
					}
				} else {
					strict := memoryconfig.ForkedViewRecoveryStrict
					if _, err := f.server.updateMemoryAutomaticPreferences(f.stream.OwnerID, nil, &strict); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := 0
			f.server.memoryExtraction.factory = func(_ memorytools.Scope, mode string, view *agentruntime.ModelRequest) memoryextract.Extractor {
				if mode != "compact" || view != nil {
					t.Fatal("recovery changed its effective mode/view")
				}
				return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
					calls++
					if name == "strict-after-model" {
						revoke()
					}
					return durableMemoryFixtureAppend(), nil
				})
			}
			if strings.HasSuffix(name, "before-model") {
				revoke()
			}
			if name == "strict-before-commit" {
				revoke()
				if err := f.server.withAutomaticMemoryCommit(t.Context(), f.stream.OwnerID, func() error { t.Error("revoked recovery crossed commit fence"); return nil }, decoded.Completion.ModeDecision); !errors.Is(err, workspace.ErrAgentMemoryMutationForbidden) {
					t.Fatalf("commit=%v", err)
				}
			} else {
				if err := f.server.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, outbox.ErrDeliverySettled) {
					t.Fatalf("delivery=%v", err)
				}
				if expected := map[bool]int{true: 1, false: 0}[name == "strict-after-model"]; calls != expected {
					t.Fatalf("calls=%d want=%d", calls, expected)
				}
			}
			requireNoDurableMemoryEffects(t, f)
		})
	}
}

func TestMemoryRecoveryPreferenceAPIUsesOneAtomicPolicyAuthority(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	key := userPreferenceSettingKey(memoryAutoExtractionSettingKey, f.stream.OwnerID)
	if _, err := f.server.settingsStore.Set(key, true); err != nil {
		t.Fatal(err)
	}
	app := f.server.Handler()
	legacy := memoryCompatJSON(t, app, http.MethodGet, "/api/memory/auto-enabled", nil, f.stream.OwnerID, http.StatusOK)
	if legacy["forked_view_recovery"] != "strict" {
		t.Fatal("legacy bool inferred recovery consent")
	}
	updated := memoryCompatJSON(t, app, http.MethodPut, "/api/memory/auto-enabled", map[string]any{"enabled": true, "forked_view_recovery": "compact-on-oversize"}, f.stream.OwnerID, http.StatusOK)
	if updated["forked_view_recovery"] != "compact-on-oversize" {
		t.Fatal("explicit policy missing")
	}
	memoryCompatJSON(t, app, http.MethodPut, "/api/memory/auto-enabled", map[string]any{"enabled": false}, f.stream.OwnerID, http.StatusOK)
	read := memoryCompatJSON(t, app, http.MethodGet, "/api/memory/auto-enabled", nil, f.stream.OwnerID, http.StatusOK)
	if read["enabled"] != false || read["forked_view_recovery"] != "compact-on-oversize" {
		t.Fatal("enabled-only PUT lost existing policy")
	}
	context := memoryCompatJSON(t, app, http.MethodGet, "/api/memory/context", nil, f.stream.OwnerID, http.StatusOK)
	if context["forked_view_recovery"] != read["forked_view_recovery"] {
		t.Fatal("context uses another policy authority")
	}
	if enabled, err := f.server.memoryAutoExtractionEnabled(f.stream.OwnerID); err != nil || enabled {
		t.Fatalf("legacy bool getter=%t %v", enabled, err)
	}
	for _, invalid := range []any{nil, "", true, "fallback-any-error"} {
		memoryCompatJSON(t, app, http.MethodPut, "/api/memory/auto-enabled", map[string]any{"enabled": true, "forked_view_recovery": invalid}, f.stream.OwnerID, http.StatusBadRequest)
	}
	persisted, _, err := f.server.settingsStore.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(persisted.Value)
	var after map[string]any
	if json.Unmarshal(raw, &after) != nil || after["enabled"] != false || after["forked_view_recovery"] != "compact-on-oversize" {
		t.Fatal("invalid policy request mutated stored preference")
	}
}

func TestMemoryLegacyOversizeDiagnosticDoesNotAuthorizeRecovery(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	// A missing legacy policy keeps the original strict durable diagnostic.
	f.server.memoryConfig.ForkedViewRecovery = memoryconfig.ForkedViewRecoveryStrict
	rememberOversizedRecoveryFixture(t, f)
	f.server.memoryExtraction.tasks = nil
	if _, err := settleDurableMemoryFixture(t, f, "Preserve original completion identity.", nil); err != nil {
		t.Fatal(err)
	}
	event := claimDurableMemoryFixture(t, f.store, "legacy-diagnostic-worker")
	var payload map[string]any
	if json.Unmarshal(event.Payload, &payload) != nil {
		t.Fatal("invalid fixture")
	}
	delete(payload["completion"].(map[string]any), "mode_decision")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	// In-memory transport envelope only: no legacy production row is rewritten.
	event.Payload = raw
	auto := memoryconfig.ForkedViewRecoveryCompact
	if _, err := f.server.updateMemoryAutomaticPreferences(f.stream.OwnerID, nil, &auto); err != nil {
		t.Fatal(err)
	}
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		t.Fatal("legacy diagnostic invoked a new model")
		return nil
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("delivery=%v", err)
	}
	requireNoDurableMemoryEffects(t, f)
}
