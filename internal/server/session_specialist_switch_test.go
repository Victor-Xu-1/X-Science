package server

import (
	"errors"
	"testing"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

// Exercise the existing switch behavior, not a nonexistent new API. Saving a
// next-message hint is not evidence that the actual Agent authority changed.
func TestSessionSpecialistSwitchNeverReportsUnappliedSuccess(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "specialist-project", "specialist-owner")
	frame, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "specialist-frame", ProjectID: "specialist-project", AgentName: "OPERON", Status: "completed", ConversationType: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAgent(workspace.CreateAgentInput{
		ID: "specialist-b-id", UserID: "specialist-owner", Name: "SPECIALIST_B",
		DisplayName: "Synthetic B", Description: "Synthetic switch fixture.", SystemPrompt: "Synthetic B authority only.",
	}); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(t.Context(), frame.ID)
	if err != nil || !found {
		t.Fatalf("load actual frame authority: found=%t err=%v", found, err)
	}
	result, err := app.kernelHostSwitchAgent(t.Context(), access, "SPECIALIST_B")
	if err != nil {
		var rejection *kernelruntime.HostCallError
		if !errors.As(err, &rejection) || rejection.Code != "permission_denied" && rejection.Code != "unavailable" && rejection.Code != "not_supported" {
			t.Fatalf("switch failed outside the explicit authority contract: %v", err)
		}
		// Without an admitted execution a structured rejection is legitimate;
		// it must not leave the old hint-only mutation behind.
		metadata, _, readErr := store.GetFrameRuntimeMetadata(frame.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if original, ok := metadata.ContextData["_original_input"].(map[string]any); ok && original["target_agent"] == "SPECIALIST_B" {
			t.Fatalf("rejected switch still persisted an unowned target: %v", err)
		}
		return
	}
	current, found, err := store.GetFrame(frame.ID)
	if err != nil || !found {
		t.Fatalf("read actual frame: found=%t err=%v", found, err)
	}
	reply, ok := result.(map[string]any)
	if !ok || current.AgentName == "OPERON" && (reply["switched"] == true || reply["status"] == "applied") {
		t.Fatalf("hint-only switch falsely reported runtime application: actual=%q reply=%#v", current.AgentName, result)
	}
}
