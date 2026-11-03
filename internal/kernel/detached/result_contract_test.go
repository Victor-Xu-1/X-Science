package detached

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/processsupervisor"
)

func resultAuthorityForTest(t *testing.T, result ExecutionResultV1, version int) (workspace.DetachedKernelExecution, workspace.KernelExecutionResultReceipt) {
	t.Helper()
	request := workspace.KernelDetachedExecutionRequestV1{Version: 1, OperationID: "operation", ExecutionID: result.ExecutionID,
		OwnerUserID: "owner", ProjectID: "project", RootFrameID: "root", RootFrameIncarnationID: "root-incarnation", FrameID: "frame", FrameIncarnationID: "frame-incarnation",
		KernelID: result.KernelID, KernelGeneration: 1, ToolCallID: result.ToolCallID, ToolName: "python", Language: result.Language, KernelKind: result.KernelKind,
		Environment: result.Environment, Code: "print('fixture')", OutputLimitBytes: 1 << 20, Origin: "agent", HostCallMethods: []string{}}
	binding := workspace.KernelDetachedExecutionBindingV2{BackendID: "backend", BackendGeneration: 2, ExecutorInstanceID: "executor", SessionSpecSHA256: strings.Repeat("a", 64)}
	var requestPayload any = request
	if version == 2 {
		requestPayload = workspace.KernelDetachedExecutionRequestV2{Version: 2, Request: request, Binding: binding, ResultContract: workspace.KernelExecutionResultContract{ResultVersion: 2, HostResourceEvidenceVersion: 1}}
	}
	raw, err := json.Marshal(requestPayload)
	if err != nil {
		t.Fatal(err)
	}
	execution := workspace.DetachedKernelExecution{ExecutionID: result.ExecutionID, OperationID: request.OperationID, BackendID: binding.BackendID, BackendGeneration: binding.BackendGeneration,
		RequestJSON: string(raw), RequestSHA256: resultSHAForTest(raw), TerminalReceiptID: "receipt"}
	doc, err := workspace.ReadDetachedExecutionDocument(execution)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(executorResultPayload(result, doc, execution.RequestSHA256, nil))
	if err != nil {
		t.Fatal(err)
	}
	receipt := workspace.KernelExecutionResultReceipt{ReceiptID: "receipt", ExecutionID: execution.ExecutionID, OperationID: execution.OperationID, BackendID: execution.BackendID,
		BackendGeneration: execution.BackendGeneration, ResultJSON: string(encoded), ResultSHA256: resultSHAForTest(encoded), StartedAt: result.StartedAt, FinishedAt: result.FinishedAt}
	return execution, receipt
}

func resultSHAForTest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func TestReaderFirstAcceptsBoundV2Result(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	result := ExecutionResultV1{Version: 1, ExecutionID: "execution-v2", ToolCallID: "tool-v2", KernelID: "kernel-v2", KernelKind: "analysis", Language: "python", Environment: "python", StartedAt: now, FinishedAt: now.Add(time.Second)}
	execution, receipt := resultAuthorityForTest(t, result, 2)
	loaded, cleanup, err := LoadExecutionResult(t.TempDir(), receipt, execution)
	if err != nil || cleanup != "" || loaded.ExecutionID != result.ExecutionID || loaded.HostResources.Reason != "no_sampler" {
		t.Fatalf("loaded=%#v cleanup=%q err=%v", loaded, cleanup, err)
	}
	t.Run("spool", func(t *testing.T) {
		large := result
		large.Response.Stdout = strings.Repeat("x", 1048577)
		execution, receipt := resultAuthorityForTest(t, large, 2)
		spool := t.TempDir()
		encoded := []byte(receipt.ResultJSON)
		fullSHA := resultSHAForTest(encoded)
		if err := os.WriteFile(filepath.Join(spool, fullSHA+".json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
		pointer, _ := json.Marshal(spoolPointerV1{Version: 1, ExecutionID: result.ExecutionID, Kind: "kernel-spool-v1", SHA256: fullSHA, Bytes: len(encoded)})
		receipt.ResultJSON, receipt.ResultSHA256, receipt.ResultRef = string(pointer), resultSHAForTest(pointer), "kernel-spool-sha256:"+fullSHA
		got, cleanup, err := LoadExecutionResult(spool, receipt, execution)
		if err != nil || cleanup != filepath.Join(spool, fullSHA+".json") || len(got.Response.Stdout) != len(large.Response.Stdout) {
			t.Fatalf("spool err=%v cleanup=%s", err, cleanup)
		}
	})
	for _, field := range []string{"binding", "generation", "session_sha", "request_sha256", "outcome", "version", "extra"} {
		t.Run("reject_"+field, func(t *testing.T) {
			var payload ExecutionResultV2
			if err := json.Unmarshal([]byte(receipt.ResultJSON), &payload); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "binding":
				payload.Binding.ExecutorInstanceID = "replacement-executor"
			case "generation":
				payload.Binding.BackendGeneration++
			case "session_sha":
				payload.Binding.SessionSpecSHA256 = strings.Repeat("c", 64)
			case "request_sha256":
				payload.RequestSHA256 = strings.Repeat("b", 64)
			case "outcome":
				payload.Outcome.ToolCallID = "foreign-tool"
			case "version":
				payload.Version = 1
			}
			raw, _ := json.Marshal(payload)
			if field == "extra" {
				raw = append(raw[:len(raw)-1], []byte(`,"extra":true}`)...)
			}
			changed := receipt
			changed.ResultJSON, changed.ResultSHA256 = string(raw), resultSHAForTest(raw)
			if _, _, err := LoadExecutionResult(t.TempDir(), changed, execution); !errors.Is(err, ErrExecutionResultContractConflict) {
				t.Fatalf("accepted mismatch %s: %v", field, err)
			}
		})
	}
}

func TestReaderFirstV1NeverAcquiresGuestHostAuthority(t *testing.T) {
	now := time.Now().UTC()
	result := ExecutionResultV1{Version: 1, ExecutionID: "old-execution", ToolCallID: "old-tool", KernelID: "old-kernel", KernelKind: "analysis", Language: "python", Environment: "python", StartedAt: now, FinishedAt: now.Add(time.Second),
		Response: kernelruntime.Response{Trace: map[string]any{"resource_pressure": map[string]any{"status": "pressured", "limit_bytes": 100}, "host_resources": map[string]any{"source": "host", "version": 2}}}}
	execution, receipt := resultAuthorityForTest(t, result, 1)
	loaded, _, err := LoadExecutionResult(t.TempDir(), receipt, execution)
	if err != nil || loaded.HostResources.Reason != "legacy_v1" || loaded.HostResources.PressureFailure != nil {
		t.Fatalf("legacy provenance err=%v resources=%#v", err, loaded.HostResources)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != receipt.ResultJSON {
		t.Fatal("V1 result bytes changed")
	}
	var bad map[string]any
	json.Unmarshal(encoded, &bad)
	bad["host_resources"] = map[string]any{"version": 1, "status": "unavailable", "reason": "no_sampler"}
	raw, _ := json.Marshal(bad)
	receipt.ResultJSON, receipt.ResultSHA256 = string(raw), resultSHAForTest(raw)
	if _, _, err := LoadExecutionResult(t.TempDir(), receipt, execution); err == nil {
		t.Fatal("V1 decoder was loosened")
	}
}

func TestReaderFirstV2SerializerUsesOnlyActualHostPressure(t *testing.T) {
	result := ExecutionResultV1{Version: 1}
	doc := workspace.KernelDetachedExecutionDocument{DocumentVersion: 2}
	pressure := processsupervisor.MemoryPressure{Status: "pressured", LimitBytes: 100, FullStallPercent: 90}
	result.Response.Trace = map[string]any{"resource_pressure": pressure}
	plain := executorResultPayload(result, doc, "request-sha", errors.New("guest error")).(ExecutionResultV2)
	if plain.HostResources.PressureFailure != nil || plain.HostResources.Reason != "no_sampler" {
		t.Fatal("guest became host evidence")
	}
	actual := executorResultPayload(result, doc, "request-sha", &kernelruntime.ResourcePressureFailure{Observation: pressure}).(ExecutionResultV2)
	if actual.HostResources.PressureFailure == nil || actual.HostResources.Validate() != nil {
		t.Fatal("typed host failure was lost")
	}
}
