package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

type hostGrantContextRecordingClient struct {
	requests []agentruntime.ModelRequest
	fail     bool
}

func (client *hostGrantContextRecordingClient) Complete(_ context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	client.requests = append(client.requests, request)
	if client.fail {
		return agentruntime.ModelResponse{}, errors.New("controlled provider failure")
	}
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "controlled completion"}}, nil
}

func (client *hostGrantContextRecordingClient) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	return client.Complete(ctx, request)
}

func hostGrantContextProjectionFixture(t *testing.T) (*agentSaveArtifactsFixture, runnerHostGrantContextClient, agentruntime.ModelRequest) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	raw, _ := seedRunnerHostGrantDiscovery(t, f, "projection-discovery")
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	request := agentruntime.ModelRequest{
		Tools: agentPermissionToolSchemas()[:1],
		Messages: []agentruntime.Message{{Role: "user", Content: "Inspect current local discovery."},
			{Role: "tool", ToolCallID: "projection-discovery", Content: string(raw)}},
	}
	return f, runnerHostGrantContextClient{server: f.server, run: run, delegate: &hostGrantContextRecordingClient{}}, request
}

func TestRunnerHostGrantContextProjectsOnlyProvenCurrentDiscovery(t *testing.T) {
	for _, state := range []string{"current", "changed", "context-missing", "context-truncated", "legacy", "failed", "settings-unavailable"} {
		t.Run(state, func(t *testing.T) {
			f, client, request := hostGrantContextProjectionFixture(t)
			want := hostGrantContextMissingNotice
			switch state {
			case "current":
				want = ""
			case "changed":
				if _, err := f.server.upsertHostGrant(f.stream.OwnerID, t.TempDir(), "read"); err != nil {
					t.Fatal(err)
				}
				want = hostGrantContextChangedNotice
			case "context-missing":
				request.Messages = request.Messages[:1]
			case "context-truncated":
				var partial map[string]any
				_ = json.Unmarshal([]byte(request.Messages[1].Content), &partial)
				partial["grants"] = []any{}
				raw, _ := json.Marshal(partial)
				request.Messages[1].Content = string(raw)
			case "legacy", "failed":
				// A synthetic historical-contract fixture, not claimed execution.
				// Append through the repository; never rewrite an old receipt.
				payload := map[string]any{"status": "completed", "toolName": listHostGrantsToolName,
					"toolCallId": "latest-unknown-discovery", "toolPhase": "completed", "toolResult": map[string]any{"grants": []any{}}}
				if state == "failed" {
					payload["status"], payload["toolPhase"] = "failed", "failed"
					payload["toolResult"] = map[string]any{"error": "controlled discovery unavailable"}
				}
				raw, _ := json.Marshal(payload)
				if _, _, _, err := f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
					Claim: f.claim, ClientMessageID: "latest-unknown-discovery", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
				}); err != nil {
					t.Fatal(err)
				}
			case "settings-unavailable":
				saved := f.server.settingsStore
				f.server.settingsStore = nil
				defer func() { f.server.settingsStore = saved }()
			}
			before, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
			if err != nil {
				t.Fatal(err)
			}
			original := append([]agentruntime.Message(nil), request.Messages...)
			prepared, err := client.prepare(t.Context(), request)
			if err != nil || !reflect.DeepEqual(request.Messages, original) {
				t.Fatalf("projection failed or mutated the caller's messages: %v", err)
			}
			extra := len(prepared.Messages) - len(request.Messages)
			if want == "" && extra != 0 || want != "" && (extra != 1 || prepared.Messages[len(prepared.Messages)-1].Content != want) {
				t.Fatalf("projection extra=%d want notice=%q", extra, want)
			}
			if extra == 1 && (prepared.Messages[len(prepared.Messages)-1].Role != "system" || strings.Contains(want, f.projectPath)) {
				t.Fatal("context notice became a user message or disclosed a directory")
			}
			after, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
			if err != nil || after.EventID != before.EventID || string(after.PayloadJSON) != string(before.PayloadJSON) {
				t.Fatal("context preparation consumed or rewrote discovery evidence")
			}
		})
	}
}

func TestRunnerHostGrantContextFailureCancellationAndQueueDoNotConsumeProof(t *testing.T) {
	f, client, request := hostGrantContextProjectionFixture(t)
	if _, err := f.server.upsertHostGrant(f.stream.OwnerID, t.TempDir(), "read"); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil {
		t.Fatal(err)
	}
	// A later publication changes the read cutoff, not the running input.
	appendSkillControlInput(t, f, "queued-context-only", "A later ordinary input.", false)
	probe := &hostGrantContextRecordingClient{fail: true}
	client.delegate = probe
	if _, err := client.Complete(t.Context(), request); err == nil {
		t.Fatal("controlled provider failure was swallowed")
	}
	probe.fail = false
	if _, err := client.CompleteStream(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	if len(probe.requests) != 2 {
		t.Fatalf("calls=%d; projection must not add retries", len(probe.requests))
	}
	for _, actual := range probe.requests {
		if actual.Messages[len(actual.Messages)-1].Content != hostGrantContextChangedNotice {
			t.Fatal("failed request or queued input consumed the unchanged discovery proof")
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.CompleteStream(cancelled, request, nil); !errors.Is(err, context.Canceled) || len(probe.requests) != 2 {
		t.Fatalf("cancelled context reached provider: err=%v calls=%d", err, len(probe.requests))
	}
	after, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || after.EventID != before.EventID || string(after.PayloadJSON) != string(before.PayloadJSON) || after.Input.EventID != before.Input.EventID {
		t.Fatal("provider/cancellation/queue advanced discovery or replaced the running input")
	}
}

func TestRunnerHostGrantContextExcludesHiddenAuxiliaryAndStaleClaims(t *testing.T) {
	for _, state := range []string{"hidden-control", "automatic", "auxiliary", "tool-unavailable", "foreign-owner", "stale-claim"} {
		t.Run(state, func(t *testing.T) {
			f, client, request := hostGrantContextProjectionFixture(t)
			if _, err := f.server.upsertHostGrant(f.stream.OwnerID, t.TempDir(), "read"); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			wantError := false
			switch state {
			case "hidden-control":
				finishSkillControlInput(t, f)
				appendSkillControlInput(t, f, "hidden-projection-control", "Save this procedure.", true)
				client.run = claimSkillControlInput(t, f)
			case "automatic":
				// Unit guard for an automatic source, not a claim of resume acceptance.
				client.run.Transcript.Claim.ResumeSource = transcriptstore.ResumeSourceRetry
			case "auxiliary":
				ctx = context.WithValue(ctx, auxiliaryContextUsageKey{}, true)
			case "tool-unavailable":
				request.Tools = nil
			case "foreign-owner":
				client.run.Transcript.Claim.OwnerID = "foreign-owner"
				wantError = true
			case "stale-claim":
				client.run.Transcript.Claim.ClaimToken = "stale-token"
				wantError = true
			}
			probe := &hostGrantContextRecordingClient{}
			client.delegate = probe
			_, err := client.Complete(ctx, request)
			if wantError {
				if err == nil || len(probe.requests) != 0 {
					t.Fatal("foreign or stale context reached the provider")
				}
				return
			}
			if err != nil || len(probe.requests) != 1 || !reflect.DeepEqual(probe.requests[0], request) {
				t.Fatalf("ineligible input was changed or blocked: %v calls=%d", err, len(probe.requests))
			}
		})
	}
}
