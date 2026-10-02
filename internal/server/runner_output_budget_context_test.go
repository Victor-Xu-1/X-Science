package server

import (
	"reflect"
	"synon-go/internal/agentruntime"
	eventjournal "synon-go/internal/persistence/journal"
	"testing"
)

func TestOutputBudgetRecoveryDoesNotRewriteTaskInstructions(t *testing.T) {
	entries := []eventjournal.Entry{{Message: eventjournal.Message{"type": "user_message", "role": "user", "content": "Prepare a detailed report."}}}
	before := requireProviderReplayMessages(t, "system", entries)
	entries = append(entries, eventjournal.Entry{Message: eventjournal.Message{"type": "runner_checkpoint", "status": "interrupted", "reason_code": sessionRunnerProviderOutputTokenLimitReasonCode}})
	after := requireProviderReplayMessages(t, "system", entries)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("budget recovery altered task instructions: before=%#v after=%#v", before, after)
	}
	state := &sessionRunnerProviderContinuationState{}
	state.Content.WriteString("已接受的内容\n")
	before = appendProviderContinuationContext(nil, state)
	state.ConsecutiveNoProgress = 5
	after = appendProviderContinuationContext(nil, state)
	if !reflect.DeepEqual(before, after) || after[0].Content != "已接受的内容\n" {
		t.Fatalf("continuation changed its accepted prefix or added instructions: %#v", after)
	}
}

func TestOutputBudgetContextExcludesRuntimeBudgetPreference(t *testing.T) {
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Keep the original task."}}}
	before, err := outputBudgetContext(request)
	if err != nil {
		t.Fatal(err)
	}
	request.UseProviderDefaultOutputBudget = true
	request.MaxTokens = 4096
	after, err := outputBudgetContext(request)
	if err != nil || after != before {
		t.Fatalf("runtime allowance changed semantic context identity: before=%s after=%s err=%v", before, after, err)
	}
}
