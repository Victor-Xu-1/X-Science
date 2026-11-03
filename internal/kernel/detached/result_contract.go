package detached

import (
	"encoding/json"
	"errors"
	"fmt"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/processsupervisor"
)

var ErrExecutionResultContractConflict = errors.New("detached kernel result contract conflicts with immutable execution")

type ExecutionResultV2 struct {
	Version       int                                         `json:"version"`
	Outcome       ExecutionResultV1                           `json:"outcome"`
	Binding       workspace.KernelDetachedExecutionBindingV2  `json:"binding"`
	RequestSHA256 string                                      `json:"request_sha256"`
	HostResources processsupervisor.ExecutionMemoryEvidenceV1 `json:"host_resources"`
}

type ExecutionResult struct {
	ExecutionResultV1
	HostResources processsupervisor.ExecutionMemoryEvidenceV1
}

type executorResultDocument interface{ executionIdentity() string }

func (r ExecutionResultV1) executionIdentity() string { return r.ExecutionID }
func (r ExecutionResultV2) executionIdentity() string { return r.Outcome.ExecutionID }

// This reader-first executor has no sampler. Only an immutable V2 request can
// select V2 serialization; normal session/admission defaults remain V1.
func executorResultPayload(result ExecutionResultV1, doc workspace.KernelDetachedExecutionDocument, requestSHA string, outcomeErr error) executorResultDocument {
	if doc.DocumentVersion != 2 {
		return result
	}
	resources := processsupervisor.UnavailableExecutionMemoryEvidence("no_sampler")
	var pressure *kernelruntime.ResourcePressureFailure
	if errors.As(outcomeErr, &pressure) && processsupervisor.ValidHostMemoryPressure(pressure.Observation) {
		observation := pressure.Observation
		resources.PressureFailure = &observation
	}
	return ExecutionResultV2{Version: 2, Outcome: result, Binding: doc.Binding, RequestSHA256: requestSHA, HostResources: resources}
}

func decodeExecutionResult(raw []byte, receipt workspace.KernelExecutionResultReceipt, execution workspace.DetachedKernelExecution) (ExecutionResult, error) {
	doc, err := workspace.ReadDetachedExecutionDocument(execution)
	if err != nil || receipt.ReceiptID == "" || execution.TerminalReceiptID != receipt.ReceiptID ||
		receipt.ExecutionID != execution.ExecutionID || receipt.OperationID != execution.OperationID ||
		receipt.BackendID != execution.BackendID || receipt.BackendGeneration != execution.BackendGeneration {
		return ExecutionResult{}, ErrExecutionResultContractConflict
	}
	result := ExecutionResult{HostResources: processsupervisor.UnavailableExecutionMemoryEvidence("legacy_v1")}
	switch doc.DocumentVersion {
	case 1:
		if err := decodeClosedResultJSON(raw, &result.ExecutionResultV1); err != nil {
			return ExecutionResult{}, fmt.Errorf("%w: invalid V1 payload", ErrExecutionResultContractConflict)
		}
	case 2:
		var v2 ExecutionResultV2
		if err := decodeClosedResultJSON(raw, &v2); err != nil || v2.Version != 2 ||
			v2.Binding != doc.Binding || v2.RequestSHA256 != execution.RequestSHA256 ||
			v2.HostResources.Reason == "legacy_v1" || v2.HostResources.Validate() != nil {
			return ExecutionResult{}, fmt.Errorf("%w: invalid V2 payload or binding", ErrExecutionResultContractConflict)
		}
		canonical, err := json.Marshal(v2)
		if err != nil || string(canonical) != string(raw) {
			return ExecutionResult{}, fmt.Errorf("%w: noncanonical V2 payload", ErrExecutionResultContractConflict)
		}
		result.ExecutionResultV1, result.HostResources = v2.Outcome, v2.HostResources
	default:
		return ExecutionResult{}, ErrExecutionResultContractConflict
	}
	if result.Version != 1 || result.ExecutionID != doc.ExecutionID || result.KernelID != doc.KernelID ||
		result.ToolCallID != doc.ToolCallID || result.KernelKind != doc.KernelKind || result.Language != doc.Language ||
		result.Environment != doc.Environment || (result.Response.ID != "" && result.Response.ID != doc.ExecutionID) ||
		result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) ||
		!result.StartedAt.Equal(receipt.StartedAt) || !result.FinishedAt.Equal(receipt.FinishedAt) {
		return ExecutionResult{}, fmt.Errorf("%w: outcome identity or time differs", ErrExecutionResultContractConflict)
	}
	return result, nil
}
