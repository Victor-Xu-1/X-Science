package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRuntimeSkillReplayPromptRequiresCompleteText(t *testing.T) {
	const text = "Before< scope alpha >After"
	encoded, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		value   any
		want    string
		compact bool
	}{
		{"inline readable string", text, text, false},
		{"structured full prompt", map[string]any{"prompt": text}, text, false},
		{"encoded blob string", string(encoded), text, false},
		{"encoded structured prompt", `{"prompt":"Before< scope alpha >After"}`, text, false},
		{"metadata is not a prompt", map[string]any{"skill": "fixture", "body_hash": "digest"}, "", false},
		{"preview is not a prompt", map[string]any{"preview": text}, "", false},
		{"non string prompt", map[string]any{"prompt": 1}, "", false},
		{"compact acknowledgement", map[string]any{"status": "already_loaded", "loaded": true, "reused": true, "skill": "fixture"}, "", true},
		{"foreign compact name", map[string]any{"status": "already_loaded", "loaded": true, "reused": true, "skill": "other-fixture"}, "", false},
		{"unknown compact lookalike", map[string]any{"status": "already_loaded", "reused": true}, "", false},
		{"oversized prompt", strings.Repeat("x", maxRuntimeSkillContractBytes+1), "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := runtimeSkillReplaySourceReader{}
			got, compact, err := reader.prompt(context.Background(), transcriptstore.RunnerReplayEvent{},
				eventjournal.Message{"toolResult": test.value}, &agentruntime.SkillLoadReceipt{SkillName: "fixture"})
			if err != nil || got != test.want || compact != test.compact {
				t.Fatalf("full prompt proof=%q compact=%t err=%v", got, compact, err)
			}
		})
	}
}

func TestRuntimeSkillReplayPayloadUnavailablePreservesUnexpectedIO(t *testing.T) {
	unexpected := errors.New("synthetic storage read failure")
	if !runtimeSkillReplayPayloadUnavailable(errRunnerLargeToolResultUnavailable) {
		t.Fatal("missing historical payload should retain normal explicit reload")
	}
	if runtimeSkillReplayPayloadUnavailable(unexpected) ||
		runtimeSkillReplayPayloadUnavailable(errors.Join(errRunnerLargeToolResultConflict, unexpected)) ||
		runtimeSkillReplayPayloadUnavailable(errors.Join(errRunnerLargeToolResultUnavailable, context.Canceled)) {
		t.Fatal("an I/O or cancellation failure disappeared behind a recoverable evidence diagnostic")
	}
}
