package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerToolIdentityScopeSurvivesCompactedAttemptHandoff(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{
		SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), ClaimToken: fixture.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim},
	}
	var firstDescriptor agentruntime.LargeToolResultDescriptor
	var firstCallID string
	var firstSourceEventID int64
	for invocation := 0; invocation < 2; invocation++ {
		ctx := withTranscriptRunnerChatRun(context.Background(), run)
		entries, err := fixture.server.loadTranscriptRunnerReplay(ctx, run.Transcript, 100, 100)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := sessionEntriesToProviderMessages("system", entries)
		if err != nil {
			t.Fatal(err)
		}
		if invocation == 1 {
			if _, _, compacted := latestCompactModelContext(entries); !compacted {
				t.Fatal("second invocation must use a persisted compact checkpoint")
			}
			for _, message := range messages {
				if len(message.ToolCalls) != 0 {
					t.Fatal("compacted provider window unexpectedly retained prior call IDs")
				}
			}
		}
		options := SessionRunnerChatOptions{
			SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID, OutputLimitBytes: 16_000,
		}
		schemas := []agentruntime.ToolSchema{{Name: "web_fetch"}}
		engine := fixture.server.newAgentRuntimeEngineWithContext(ctx, options, schemas)
		if engine.ToolCallIdentityPolicy != agentruntime.DurableToolCallIdentity || engine.LargeToolResults == nil {
			t.Fatal("shared runner factory omitted durable identity/result authority")
		}
		engine.Model = &runnerIdentityScopeModel{}
		value := map[string]any{"ok": true, "observation": invocation, "body": strings.Repeat("immutable observation ", 1500)}
		raw, _ := json.Marshal(value)
		var canonical agentruntime.ToolCall
		engine.Tools = agentruntime.FuncToolGateway(func(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
			canonical = call
			return agentruntime.ToolResult{Value: value}, nil
		})
		engine.OnEventError = func(event agentruntime.Event) error {
			if event.Type == agentruntime.EventModelResponse && len(event.ToolCalls) > 0 {
				return fixture.server.checkpointChatModelToolCalls(options, run, event.ToolCalls)
			}
			return fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, event)
		}
		result, err := engine.Run(ctx, agentruntime.RunRequest{Messages: agentRuntimeMessagesFromChat(messages), Tools: schemas})
		if err != nil || result.FinalMessage.Content != "done" || canonical.ID == "" {
			t.Fatalf("invocation %d: result=%#v call=%#v err=%v", invocation, result, canonical, err)
		}
		input := agentruntime.LargeToolResultInput{
			ToolCall: canonical, RawJSON: raw, Outcome: agentruntime.ToolResultSucceeded, MaxInlineBytes: 16_000,
		}
		authority := runnerLargeToolResultAuthority{server: fixture.server}
		descriptor, err := authority.Externalize(ctx, input)
		if err != nil {
			t.Fatalf("same source must replay its exact immutable result: %v", err)
		}
		replayed, err := authority.Externalize(ctx, input)
		if err != nil || replayed != descriptor {
			t.Fatalf("same-source replay changed evidence: first=%#v replay=%#v err=%v", descriptor, replayed, err)
		}
		changed := input
		changed.RawJSON = []byte(`{"ok":true,"body":"` + strings.Repeat("changed evidence ", 1500) + `"}`)
		if _, err := authority.Externalize(ctx, changed); !errors.Is(err, errRunnerLargeToolResultConflict) {
			t.Fatalf("same-source changed bytes must conflict: %v", err)
		}
		assertLargeResultExactContentHTTP(t, fixture.server.Handler(), fixture.stream.OwnerID, descriptor, raw)
		if invocation == 1 {
			if canonical.ID == firstCallID || run.ToolSourceEventIDs[canonical.ID] == firstSourceEventID ||
				descriptor.ArtifactID == firstDescriptor.ArtifactID || descriptor.VersionID == firstDescriptor.VersionID {
				t.Fatal("new source reused prior call/evidence identity")
			}
			assertLargeResultLedgerCounts(t, fixture.db, 2, 0)
			continue
		}
		firstCallID, firstDescriptor = canonical.ID, descriptor
		firstSourceEventID = run.ToolSourceEventIDs[canonical.ID]
		compactPayload := mustMarshalRawMessage(map[string]any{
			"toolPhase": "auto_compact", "modelContext": map[string]any{"summary": "The prior observation is stored."},
		})
		if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
			Claim: run.Transcript.Claim, ClientMessageID: "identity-scope-compact", Phase: transcriptstore.RunnerPhaseExecuting,
			Resumable: true, PayloadJSON: compactPayload,
		}); err != nil {
			t.Fatal(err)
		}
		interrupted, err := fixture.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
			Claim: run.Transcript.Claim, ClientMessageID: "identity-scope-handoff", ReasonCode: "runtime_draining", AutoResume: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		reclaimed, err := fixture.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
			StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "identity-scope-resumed",
			TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
		})
		if err != nil || !reclaimed.Claimed || reclaimed.Claim.Attempt != fixture.claim.Attempt {
			t.Fatalf("same-attempt handoff=%#v err=%v", reclaimed, err)
		}
		run = &sessionRunnerChatRun{
			SessionID: fixture.stream.SessionID, Attempt: int(reclaimed.Claim.Attempt), ClaimToken: reclaimed.Claim.ClaimToken,
			Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: reclaimed.Claim},
		}
	}
}

type runnerIdentityScopeModel struct{ sent bool }

func (model *runnerIdentityScopeModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	message := agentruntime.Message{Role: "assistant", Content: "done"}
	if !model.sent {
		model.sent = true
		message.Content = ""
		message.ToolCalls = []agentruntime.ToolCall{{ID: "call_0", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.test/observation"}`)}}
	}
	return agentruntime.ModelResponse{Message: message}, nil
}
