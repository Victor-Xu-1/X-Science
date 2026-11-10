package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNativeScientificAcquisitionInheritedHooks(t *testing.T) {
	for _, response := range []string{
		`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"source denied"}}`,
		`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"url":"https://other.example.org/rewritten.csv"}}}`,
	} {
		t.Run(response, func(t *testing.T) {
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
			hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, response)
			}))
			defer hook.Close()
			if _, err := fixture.server.settingsStore.Set("hooks", map[string]any{"PreToolUse": []any{
				map[string]any{"matcher": "web_fetch", "hooks": []any{map[string]any{"type": "http", "url": hook.URL, "timeout": 5}}},
			}}); err != nil {
				t.Fatal(err)
			}
			gateway.resumeAfterApproval = true // Parent approval does not approve its source.
			result, err := gateway.Execute(ctx, call)
			if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != 0 {
				t.Fatalf("child bypassed inherited hook or changed exact URL: %#v err=%v requests=%d", result.Value, err, requests.Load())
			}
			if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); !os.IsNotExist(err) {
				t.Fatalf("blocked source published file: %v", err)
			}
		})
	}
}

func TestNativeScientificAcquisitionCancellationAndClaimLoss(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "cancel_during_real_source_response"
		if revoke {
			name = "claim_revoked_during_real_source_response"
		}
		t.Run(name, func(t *testing.T) {
			var onResponse func()
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true, func(*http.Request) { onResponse() })
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			onResponse = cancel
			if revoke {
				onResponse = func() {
					_, err := fixture.repo.CancelRunner(context.Background(), transcriptstore.CancelRunnerInput{
						StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, ExpectedAttempt: fixture.claim.Attempt,
						ClientMessageID: "revoke-at-source", ReasonCode: "user_cancelled",
					})
					if err != nil {
						t.Errorf("revoke source owner: %v", err)
					}
				}
			}
			result, err := gateway.Execute(ctx, call)
			if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != 1 {
				t.Fatalf("cancelled parent transferred bytes: %#v err=%v requests=%d", result.Value, err, requests.Load())
			}
			_, events := scientificAcquisitionEvents(t, fixture)
			for _, event := range events {
				var payload nativeScientificAcquisition
				_ = json.Unmarshal(event.Event.PayloadJSON, &payload)
				if payload.Schema == agentPublicScientificAcquisitionSchema && payload.ToolPhase == "source_acquisition_completed" {
					t.Fatal("late source response was adopted after cancellation/claim loss")
				}
			}
			if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); !os.IsNotExist(err) {
				t.Fatalf("cancelled source published file: %v", err)
			}
		})
	}
}

func TestNativeScientificAcquisitionRejectsExistingBadProvenance(t *testing.T) {
	for _, phase := range []string{"failed", "unavailable", "completed"} {
		t.Run(phase, func(t *testing.T) {
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
			_, _, input, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
			raw := mustMarshalRawMessage(map[string]any{"toolName": "web_fetch", "toolCallId": "rejected-source", "toolPhase": phase,
				"toolInput": map[string]any{"url": request.DownloadURL}, "toolResult": map[string]any{"ok": false, "error": "controlled rejection"}})
			if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
				Claim: fixture.claim, ClientMessageID: "rejected-existing-source", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.server.validateAgentPublicScientificSourceURL(ctx, fixture.stream.UID, fixture.stream.OwnerID, request); !errors.Is(err, errAgentPublicScientificFileSource) || errors.Is(err, errAgentPublicScientificSourceMissing) {
				t.Fatalf("rejected source classified as absent: %v input=%#v", err, input)
			}
			result, err := gateway.Execute(ctx, call)
			if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != 0 {
				t.Fatalf("rejected source was automatically reacquired: %#v err=%v requests=%d", result.Value, err, requests.Load())
			}
		})
	}
}

func TestNativeScientificAcquisitionUnavailableAuthorityIsNotAbsence(t *testing.T) {
	fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
	_, _, _, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
	request.SourceToolCallID = "unresolved-explicit-source"
	if _, err := fixture.server.validateAgentPublicScientificSourceURL(ctx, fixture.stream.UID, fixture.stream.OwnerID, request); err == nil || errors.Is(err, errAgentPublicScientificSourceMissing) {
		t.Fatalf("explicit source identity was treated as missing: %v", err)
	}
	request.SourceToolCallID = ""
	if _, err := fixture.server.validateAgentPublicScientificSourceURL(ctx, fixture.stream.UID, "foreign-owner", request); err == nil || errors.Is(err, errAgentPublicScientificSourceMissing) {
		t.Fatalf("unreadable owner-scoped projection was treated as missing: %v", err)
	}
	registry := fixture.server.tools
	fixture.server.tools = nil
	defer func() { fixture.server.tools = registry }()
	result, err := gateway.Execute(ctx, call)
	if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != 0 {
		t.Fatalf("unavailable canonical source tool was replaced: %#v err=%v requests=%d", result.Value, err, requests.Load())
	}
}
