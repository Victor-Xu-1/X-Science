package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	eventjournal "synon-go/internal/persistence/journal"
)

func requestBudgetFixture() (agentruntime.ModelRequest, agentruntime.ModelRequest) {
	small := agentruntime.ModelRequest{Messages: []agentruntime.Message{
		{Role: "system", Content: "required policy"},
		{Role: "user", Content: "inspect records"},
		{Role: "assistant", Content: "prior inspection"},
	}}
	large := small
	large.Tools = []agentruntime.ToolSchema{{Name: "inspect", Description: strings.Repeat("tool contract ", 500)}}
	return small, large
}

func TestRequestContextBudgetIncludesRuntimeSourcesAndToolExpansion(t *testing.T) {
	small, _ := requestBudgetFixture()
	for _, source := range []agentruntime.ContextUsageSource{
		agentruntime.ContextUsageSystemPrompt, agentruntime.ContextUsageSkills, agentruntime.ContextUsageMCP,
	} {
		t.Run(string(source), func(t *testing.T) {
			budget := &sessionRunnerRequestContextBudget{threshold: 1000, armed: true}
			if err := budget.beforeCall(context.Background(), small); err != nil {
				t.Fatal(err)
			}
			request := small
			request.Messages = append(append([]agentruntime.Message(nil), small.Messages...), agentruntime.Message{
				Role: "system", Content: strings.Repeat("loaded runtime contract ", 300), ContextUsageSource: source,
			})
			var pressure *sessionRunnerRequestContextPressureError
			if err := budget.beforeCall(context.Background(), request); !errors.As(err, &pressure) || pressure.estimated < pressure.threshold {
				t.Fatalf("pressure=%+v err=%v", pressure, err)
			}
		})
	}
	_, large := requestBudgetFixture()
	budget := &sessionRunnerRequestContextBudget{threshold: 1000, armed: true}
	err := budget.beforeCall(context.Background(), large)
	if err == nil || sessionRunnerErrorReasonCode(err) != sessionRunnerRequestContextPressureReasonCode ||
		!runnerInterruptionAutoResume(sessionRunnerRequestContextPressureReasonCode) ||
		!runnerInterruptionMayContinueSameTask(sessionRunnerRequestContextPressureReasonCode) ||
		!runnerInterruptionIsProgressBoundary(sessionRunnerRequestContextPressureReasonCode) {
		t.Fatalf("request pressure did not use durable continuation: %v", err)
	}
}

func TestRequestContextBudgetDoesNotLoopAfterIrreducibleCompaction(t *testing.T) {
	small, large := requestBudgetFixture()
	budget := &sessionRunnerRequestContextBudget{threshold: 1000, armed: false}
	for index := 0; index < 10; index++ {
		if err := budget.beforeCall(context.Background(), large); err != nil {
			t.Fatalf("already-compacted pressure retriggered at request %d: %v", index, err)
		}
	}
	if err := budget.beforeCall(context.Background(), small); err != nil {
		t.Fatal(err)
	}
	if err := budget.beforeCall(context.Background(), large); err == nil {
		t.Fatal("new below-to-above transition did not request compaction")
	}
	fixed := large
	fixed.Messages = append([]agentruntime.Message(nil), small.Messages[:2]...)
	budget = &sessionRunnerRequestContextBudget{threshold: 1000, armed: true}
	if err := budget.beforeCall(context.Background(), fixed); err != nil {
		t.Fatalf("required input/schema without archiveable history was blocked: %v", err)
	}
}

func TestRequestContextBudgetPreservesAuxiliaryCancellationAndDisabledPolicy(t *testing.T) {
	_, large := requestBudgetFixture()
	budget := &sessionRunnerRequestContextBudget{threshold: 1000, armed: true}
	if err := budget.beforeCall(withAuxiliaryContextUsage(context.Background()), large); err != nil || !budget.armed {
		t.Fatalf("presentation request consumed main-agent budget: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := budget.beforeCall(ctx, large); !errors.Is(err, context.Canceled) || !budget.armed {
		t.Fatalf("task cancellation was replaced by compaction: %v", err)
	}
	server := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	if _, err := server.settingsStore.Set(configStoreKey("autoCompactEnabled"), false); err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{}}
	if budget := newSessionRunnerRequestContextBudget(server, SessionRunnerChatOptions{}, nil, run, false); budget != nil {
		t.Fatal("explicitly disabled compaction was reenabled")
	}
}

func TestRequestContextBudgetFreshReplaySurvivesRestartWithoutNewProgress(t *testing.T) {
	entries := []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "continue inspection"}},
		{EventID: 2, Message: eventjournal.Message{"type": "runner_checkpoint", "reason_code": sessionRunnerRequestContextPressureReasonCode}},
	}
	if got := runnerContextPressureRequiringCompaction(entries); got != sessionRunnerRequestContextPressureReasonCode {
		t.Fatalf("pending request pressure=%q", got)
	}
	entries = append(entries, eventjournal.Entry{EventID: 3, Message: eventjournal.Message{
		"type": "session_compact", "trigger": "auto", "summary": "durable handoff",
	}}, eventjournal.Entry{EventID: 4, Message: eventjournal.Message{"type": "runner_checkpoint", "stage": "preparing"}})
	if !runnerReplayFreshlyCompacted(entries) || runnerContextPressureRequiringCompaction(entries) != "" {
		t.Fatal("preparation-only restart lost compact boundary")
	}
	entries = append(entries, eventjournal.Entry{EventID: 5, Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolCallId": "inspection", "toolPhase": "completed",
	}})
	if runnerReplayFreshlyCompacted(entries) {
		t.Fatal("new durable tool result was mistaken for unchanged compact replay")
	}
}

type requestBudgetForbiddenModel struct{ calls int }

func (model *requestBudgetForbiddenModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.calls++
	return agentruntime.ModelResponse{}, errors.New("provider should not be dispatched")
}

func TestRequestContextBudgetPreemptsBothProviderTransports(t *testing.T) {
	_, large := requestBudgetFixture()
	for _, streaming := range []bool{false, true} {
		provider := &requestBudgetForbiddenModel{}
		client := &sessionRunnerDynamicModelClient{
			initialReady: true, initial: sessionRunnerResolvedModelClient{client: provider, model: "test-model"},
			contextBudget: &sessionRunnerRequestContextBudget{threshold: 1000, armed: true},
		}
		var err error
		if streaming {
			_, err = client.CompleteStream(context.Background(), large, nil)
		} else {
			_, err = client.Complete(context.Background(), large)
		}
		var pressure *sessionRunnerRequestContextPressureError
		if !errors.As(err, &pressure) || provider.calls != 0 || !client.initialReady {
			t.Fatalf("stream=%t calls=%d initial=%t err=%v", streaming, provider.calls, client.initialReady, err)
		}
	}
}
