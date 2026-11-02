package server

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	runtimekv "x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

func TestProviderResponseDiagnosticsAuditRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	store := runtimekv.New(path)
	t.Cleanup(func() { _ = store.Close() })
	s := &Server{runtimeStore: store}
	const payload = `{"requestId":"observed","httpStatus":200,"promptTokens":12,"completionTokens":3,"totalTokens":15,"responseDiagnostics":{"terminalObserved":true,"rawToolCallsObserved":false,"rawToolCallCount":0,"decodedToolCallCount":0,"outcome":"decoded","incomplete":false}}`
	var record providers.AuditRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		t.Fatal(err)
	}
	s.recordSessionRunnerModelAudit("fixture-session", 7, record)
	s.recordSessionRunnerModelAudit("fixture-session", 7, providers.AuditRecord{RequestID: "legacy"})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded := runtimekv.New(path)
	t.Cleanup(func() { _ = reloaded.Close() })
	entries, err := reloaded.ListReadOnly(sessionRunnerModelAuditRuntimeNamespace)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%d err=%v; diagnostic must not add receipts", len(entries), err)
	}
	var expected map[string]any
	if err := json.Unmarshal([]byte(payload), &expected); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		value := entry.Value.(map[string]any)
		if value["sessionId"] != "fixture-session" || value["attempt"] != float64(7) {
			t.Fatalf("authority changed: %#v", value)
		}
		if value["requestId"] == "observed" {
			if !reflect.DeepEqual(value["responseDiagnostics"], expected["responseDiagnostics"]) {
				t.Fatalf("observed zero/missing distinction lost: %#v", value["responseDiagnostics"])
			}
		} else if _, exists := value["responseDiagnostics"]; exists {
			t.Fatalf("legacy unknown diagnostic was invented: %#v", value)
		}
	}
	usage, err := aggregateFrameTaskModelUsage(entries, "fixture-session", []int64{7})
	if err != nil || usage.ModelCallCount != 2 || usage.InputTokens != 12 || usage.OutputTokens != 3 || usage.TotalTokens != 15 {
		t.Fatalf("Calls/usage changed: %#v %v", usage, err)
	}
}
