package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func sourceOriginTestCall(tool, locator string) ToolCall {
	arguments, _ := json.Marshal(map[string]any{"url": locator})
	return ToolCall{Name: tool, Arguments: arguments}
}

func TestFailedToolCallGuardKeepsCanonicalOriginBudget(t *testing.T) {
	for _, variants := range [][]string{
		{"https://unavailable.example/a", "HTTPS://UNAVAILABLE.EXAMPLE:443/b?query=other", "https://unavailable.example.:00443/c#section", "https://unavailable.example/d"},
		{"http://unavailable.example/a", "http://UNAVAILABLE.EXAMPLE:80/b", "http://unavailable.example.:00080/c", "http://unavailable.example/d"},
		{"https://[2001:db8::1]/a", "https://[2001:0DB8:0:0:0:0:0:1]:443/b", "https://[2001:db8::1]:00443/c", "https://[2001:db8::1]/d"},
	} {
		t.Run(variants[0], func(t *testing.T) {
			executions := 0
			guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				executions++
				return sourceOriginUnavailable(), nil
			}))
			for index, locator := range variants {
				call := sourceOriginTestCall("web_fetch", locator)
				var input map[string]any
				_ = json.Unmarshal(call.Arguments, &input)
				input["human_description"] = fmt.Sprintf("different label %d", index)
				call.Arguments, _ = json.Marshal(input)
				result, err := guard.Execute(context.Background(), call)
				if err != nil {
					t.Fatal(err)
				}
				if index == maxTransientSourceUnavailableAttempts && !ToolResultDidNotExecute(result.Value) {
					t.Fatalf("equivalent origin reopened an exhausted budget: %#v", result.Value)
				}
			}
			if executions != maxTransientSourceUnavailableAttempts {
				t.Fatalf("equivalent origin executed %d times", executions)
			}
		})
	}
}

func TestFailedToolCallGuardIndependentSuccessDoesNotRepairUnavailableOrigin(t *testing.T) {
	guard := newFailedToolCallGuard(FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
		if strings.Contains(string(call.Arguments), "healthy.example") {
			return ToolResult{Value: map[string]any{"ok": true, "body": "verified source"}}, nil
		}
		return sourceOriginUnavailable(), nil
	}))
	for index := 0; index < maxTransientSourceUnavailableAttempts-1; index++ {
		_, _ = guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", fmt.Sprintf("https://unavailable.example/%d", index)))
	}
	if result, err := guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", "https://healthy.example/record")); err != nil || ClassifyToolResult(result.Value) != ToolResultSucceeded {
		t.Fatalf("independent success was blocked: %#v err=%v", result.Value, err)
	}
	result, err := guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", "https://unavailable.example/final-attempt"))
	if err != nil || toolResultCode(result.Value) != "source_unavailable_retry_exhausted" || ToolResultDidNotExecute(result.Value) {
		t.Fatalf("another origin's success cleared the failed source budget: %#v err=%v", result.Value, err)
	}
}

func TestFailedToolCallGuardSearchURLDoesNotSplitConnectorBudget(t *testing.T) {
	executions := 0
	guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
		executions++
		return sourceOriginUnavailable(), nil
	}), ToolSchema{Name: "future_search", Capabilities: []string{"source-discovery", "research"}})
	for index := 0; index <= maxTransientSourceUnavailableAttempts; index++ {
		result, err := guard.Execute(context.Background(), sourceOriginTestCall("future_search", fmt.Sprintf("https://unrelated-%d.example/", index)))
		if err != nil || (index == maxTransientSourceUnavailableAttempts && !ToolResultDidNotExecute(result.Value)) {
			t.Fatalf("unrelated search URL bypassed connector budget: %#v err=%v", result.Value, err)
		}
	}
	if executions != maxTransientSourceUnavailableAttempts {
		t.Fatalf("search executed %d times", executions)
	}
}

type sourceOriginNormalizedGateway struct {
	resolve    bool
	executions int
}

func (g *sourceOriginNormalizedGateway) normalized(call ToolCall) ToolCall {
	if strings.Contains(string(call.Arguments), "alias.example") {
		call.Arguments = sourceOriginTestCall(call.Name, "https://unavailable.example/record").Arguments
	}
	return call
}

func (g *sourceOriginNormalizedGateway) ToolCallExecutionIdentity(call ToolCall) (ToolCall, bool) {
	return g.normalized(call), g.resolve
}

func (g *sourceOriginNormalizedGateway) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	g.executions++
	actual := g.normalized(call)
	result := sourceOriginUnavailable()
	result.ExecutedArguments = actual.Arguments
	return result, nil
}

func TestFailedToolCallGuardSourceBudgetUsesExecutionArguments(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(fmt.Sprint(resolve), func(t *testing.T) {
			gateway := &sourceOriginNormalizedGateway{resolve: resolve}
			guard := newFailedToolCallGuard(gateway)
			for index := 0; index < maxTransientSourceUnavailableAttempts; index++ {
				if _, err := guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", fmt.Sprintf("https://alias.example/%d", index))); err != nil {
					t.Fatal(err)
				}
			}
			result, err := guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", "https://alias.example/next"))
			if err != nil || !ToolResultDidNotExecute(result.Value) || gateway.executions != maxTransientSourceUnavailableAttempts {
				t.Fatalf("actual execution origin lost its budget: %#v calls=%d err=%v", result.Value, gateway.executions, err)
			}
			result, err = guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", "https://unavailable.example/next"))
			if err != nil || !ToolResultDidNotExecute(result.Value) || gateway.executions != maxTransientSourceUnavailableAttempts {
				t.Fatalf("canonical execution origin lost its budget: %#v calls=%d err=%v", result.Value, gateway.executions, err)
			}
			result, err = guard.Execute(context.Background(), sourceOriginTestCall("web_fetch", "https://independent.example/record"))
			if err != nil || ToolResultDidNotExecute(result.Value) || gateway.executions != maxTransientSourceUnavailableAttempts+1 {
				t.Fatalf("normalized source blocked independent origin: %#v calls=%d err=%v", result.Value, gateway.executions, err)
			}
		})
	}
}

func TestFailedToolCallGuardSourcePreflightReplayDoesNotPoisonOrigin(t *testing.T) {
	// Simulate an old tool-wide budget rejection restored from canonical JSON.
	var replay map[string]any
	if err := json.Unmarshal([]byte(`{"ok":false,"code":"source_unavailable_retry_exhausted","executed":false,"preflight":true,"retryable":false,"attempts":3,"max_attempts":3,"message":"the same source connector remained unavailable after its bounded retry budget"}`), &replay); err != nil {
		t.Fatal(err)
	}
	call := sourceOriginTestCall("web_fetch", "https://independent.example/record")
	var ledger ExecutionFailureLedger
	ledger.Observe(call, []string{"source-evidence", "runtime-execution"}, replay)
	if boundary := ledger.Boundary(call); boundary != nil {
		t.Fatalf("nonexecution replay became an execution failure: %#v", boundary)
	}
	executions := 0
	guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
		executions++
		if executions == 1 {
			return ToolResult{Value: replay}, nil
		}
		return ToolResult{Value: map[string]any{"ok": true, "body": "verified source"}}, nil
	}))
	if _, err := guard.Execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	result, err := guard.Execute(context.Background(), call)
	if err != nil || executions != 2 || ClassifyToolResult(result.Value) != ToolResultSucceeded {
		t.Fatalf("old preflight replay poisoned independent source: %#v calls=%d err=%v", result.Value, executions, err)
	}
}

func TestFailedToolCallGuardAliasSuccessDoesNotClearAnotherActualSource(t *testing.T) {
	badExecutions := 0
	guard := newFailedToolCallGuard(FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
		if strings.Contains(string(call.Arguments), "/good") {
			return ToolResult{
				Value:             map[string]any{"ok": true, "body": "verified source"},
				ExecutedArguments: sourceOriginTestCall(call.Name, "https://healthy.example/record").Arguments,
			}, nil
		}
		badExecutions++
		result := sourceOriginUnavailable()
		result.ExecutedArguments = sourceOriginTestCall(call.Name, "https://unavailable.example/record").Arguments
		return result, nil
	}))
	bad := sourceOriginTestCall("web_fetch", "https://alias.example/bad")
	good := sourceOriginTestCall("web_fetch", "https://alias.example/good")
	for index := 0; index < maxTransientSourceUnavailableAttempts-1; index++ {
		if _, err := guard.Execute(context.Background(), bad); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 2; index++ {
		result, err := guard.Execute(context.Background(), good)
		if err != nil || ClassifyToolResult(result.Value) != ToolResultSucceeded {
			t.Fatalf("independent actual source was blocked: %#v err=%v", result.Value, err)
		}
		result, err = guard.Execute(context.Background(), bad)
		if err != nil {
			t.Fatal(err)
		}
		if index == 1 && (!ToolResultDidNotExecute(result.Value) || badExecutions != maxTransientSourceUnavailableAttempts) {
			t.Fatalf("healthy alias cleared another actual source failure: %#v bad executions=%d", result.Value, badExecutions)
		}
	}
}

func TestSourceOriginInvalidLocatorKeepsConnectorFallback(t *testing.T) {
	for _, locator := range []string{"", "relative/path", "file:///tmp/source", "https://", "https://host.example:invalid/", "https://host.example:70000/", "https://user:password@host.example/"} {
		if got := transientSourceFailureBucket(sourceOriginTestCall("web_fetch", locator), nil); got != "source:web_fetch" {
			t.Fatalf("invalid locator %q changed connector fallback: %q", locator, got)
		}
	}
	if got := transientSourceFailureBucket(sourceOriginTestCall("unrelated_tool", "https://host.example/"), []string{"read-only"}); got != "" {
		t.Fatalf("unrelated tool acquired a source budget: %q", got)
	}
}

func sourceOriginUnavailable() ToolResult {
	return ToolResult{Value: map[string]any{
		"ok": true, "sourceUnavailable": true, "retryable": true, "statusCode": 503,
	}}
}

func TestFailedToolCallGuardIsolatesUnavailableSourceOrigins(t *testing.T) {
	for _, tool := range []string{"web_fetch", "future_locator", "mcp__source_reader__fetch"} {
		t.Run(tool, func(t *testing.T) {
			executions := 0
			guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				executions++
				return sourceOriginUnavailable(), nil
			}), ToolSchema{Name: tool, Capabilities: []string{"source-evidence", "source-locator-read"}})
			for index := 0; index < maxTransientSourceUnavailableAttempts; index++ {
				_, err := guard.Execute(context.Background(), sourceOriginTestCall(tool, fmt.Sprintf("https://unavailable.example/record/%d?query=%d", index, index)))
				if err != nil {
					t.Fatal(err)
				}
			}
			other, err := guard.Execute(context.Background(), sourceOriginTestCall(tool, "https://independent.example/record"))
			if err != nil || executions != maxTransientSourceUnavailableAttempts+1 || ToolResultDidNotExecute(other.Value) {
				t.Fatalf("independent origin inherited another source's exhausted budget: result=%#v executions=%d err=%v", other.Value, executions, err)
			}
			blocked, err := guard.Execute(context.Background(), sourceOriginTestCall(tool, "https://unavailable.example/a-new-path?query=changed"))
			if err != nil || !ToolResultDidNotExecute(blocked.Value) || toolResultCode(blocked.Value) != "source_unavailable_retry_exhausted" || executions != maxTransientSourceUnavailableAttempts+1 {
				t.Fatalf("same origin bypassed its exhausted budget: result=%#v executions=%d err=%v", blocked.Value, executions, err)
			}
		})
	}
}
