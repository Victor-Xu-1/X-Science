package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
)

func TestContextInspectorCallFailureKindUsesActualError(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	for _, test := range []struct {
		name string
		err  error
		kind string
	}{
		{"success", nil, ""},
		{"cancelled", fmt.Errorf("transport: %w", context.Canceled), "cancelled"},
		{"deadline", fmt.Errorf("transport: %w", context.DeadlineExceeded), "deadline"},
		{"ordinary-error", errors.New("controlled transport failure"), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := recorder.begin("failure-kind-fixture", agentruntime.ModelRequest{})
			if request == nil {
				t.Fatal("real usage recorder did not begin the request")
			}
			recorder.finish(request, agentruntime.ModelResponse{}, test.err)
			inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
			var found bool
			for _, raw := range inspection["calls"].([]any) {
				call := raw.(map[string]any)
				if call["id"] != request.RequestID {
					continue
				}
				found = true
				if kind, present := call["failureKind"]; (test.kind == "" && present) || (test.kind != "" && kind != test.kind) {
					t.Fatalf("wrong recorded failure reason: got=%v expected=%q", call, test.kind)
				}
				if (test.err == nil && call["state"] != "complete") || (test.err != nil && call["state"] != "failed") {
					t.Fatalf("failure detail changed the original state contract: %+v", call)
				}
			}
			if !found {
				t.Fatal("actual recorded request disappeared from the original inspector projection")
			}
		})
	}
}

func TestContextUsageFailureKindRejectsMalformedAndSuccessfulRecords(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID,
		&sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}, SessionRunnerChatOptions{})
	request := recorder.begin("failure-wire-fixture", agentruntime.ModelRequest{})
	recorder.finish(request, agentruntime.ModelResponse{}, errors.New("controlled failure"))
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, true, "", "other", map[string]any{"kind": "cancelled"}} {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		record["failureKind"] = value
		if _, err := decodeRunnerContextUsage(runtimekv.Entry{Value: record}); err == nil {
			t.Fatalf("malformed failure kind was accepted: %#v", value)
		}
	}
	for _, state := range []string{"request", "complete"} {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		record["state"], record["failureKind"] = state, "cancelled"
		if _, err := decodeRunnerContextUsage(runtimekv.Entry{Value: record}); err == nil {
			t.Fatalf("%s call acquired a failure kind", state)
		}
	}
}
