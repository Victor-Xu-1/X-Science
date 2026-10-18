package server

import (
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

type manualNotebookPhysicalObservation struct {
	Status    string    `json:"status"`
	SampledAt time.Time `json:"sampled_at"`
	Reason    string    `json:"reason,omitempty"`
}

type manualNotebookReceipt struct {
	Schema      string                            `json:"schema"`
	RequestKind string                            `json:"request_kind"`
	Request     *manualNotebookRequest            `json:"request,omitempty"`
	Durable     workspace.ManualKernelExecution   `json:"durable"`
	Physical    manualNotebookPhysicalObservation `json:"physical"`
}

func manualNotebookRequestKind(record workspace.ManualKernelExecution) string {
	if record.Request.KernelID == "" {
		return "legacy"
	}
	return "typed-v1"
}

// Legacy requests never froze a public target. Their real binding is usable
// for read-only lookup, but must not be copied into a newly invented request.
func manualNotebookExpectedTargetFromRecord(record workspace.ManualKernelExecution) manualNotebookExpectedTarget {
	return manualNotebookExpectedTarget{FrameIncarnationID: record.FrameIncarnationID,
		RootFrameID: record.RootFrameID, RootIncarnationID: record.RootIncarnationID,
		KernelID: record.KernelID, KernelGeneration: record.KernelGeneration,
		Language: record.Request.Language, Environment: record.Request.Environment}
}

func manualNotebookRequestFromRecord(record workspace.ManualKernelExecution) manualNotebookRequest {
	return manualNotebookRequest{Schema: manualNotebookRequestSchema, RequestID: record.Request.RequestID,
		manualNotebookExpectedTarget: manualNotebookExpectedTarget{
			FrameIncarnationID: record.FrameIncarnationID, RootFrameID: record.RootFrameID, RootIncarnationID: record.RootIncarnationID,
			KernelID: record.Request.KernelID, KernelGeneration: record.Request.ExpectedKernelGeneration,
			Language: record.Request.Language, Environment: record.Request.Environment,
		}, Code: record.Request.Source, TimeoutMillis: record.Request.TimeoutMillis}
}

func manualNotebookExecutionIdentity(record workspace.ManualKernelExecution) kernelruntime.ExecutionIdentity {
	return kernelruntime.ExecutionIdentity{ExecutionID: record.ExecutionID, ToolUseID: "user-" + record.ExecutionID,
		OwnerID: record.OwnerID, ProjectID: record.ProjectID, FrameID: record.FrameID, RootFrameID: record.RootFrameID,
		FrameIncarnationID: record.FrameIncarnationID, RootFrameIncarnationID: record.RootIncarnationID,
		KernelID: record.KernelID, KernelGeneration: record.KernelGeneration, KernelKind: record.KernelKind,
		Language: record.Request.Language, Environment: record.Request.Environment, Source: record.Request.Source,
		Origin: "user", Timeout: time.Duration(record.Request.TimeoutMillis) * time.Millisecond}
}

func (s *Server) manualNotebookReceipt(record workspace.ManualKernelExecution) manualNotebookReceipt {
	physical := manualNotebookPhysicalObservation{Status: "unavailable", SampledAt: time.Now().UTC()}
	switch {
	case record.State == "terminal":
		physical.Reason = "terminal_recorded"
	case record.State == "outcome_unknown":
		physical.Reason = "terminal_receipt_unavailable"
	case record.ServerBootID != s.kernelOperationBootID:
		physical.Reason = "different_runtime_owner"
	case s.kernelManager == nil:
		physical.Reason = "runtime_unavailable"
	default:
		observation, found := s.kernelManager.ObserveExecution(manualNotebookExecutionIdentity(record))
		if !found {
			physical.Reason = "matching_registration_unavailable"
		} else if observation.Status == "running" && record.StartedAt == nil {
			// Manager dispatch begins before the worker's acknowledgement.
			physical.Reason = "start_acknowledgement_pending"
		} else {
			physical.Status, physical.SampledAt = observation.Status, observation.SampledAt
		}
	}
	receipt := manualNotebookReceipt{Schema: "x-science.manual-kernel-receipt.v1", RequestKind: manualNotebookRequestKind(record), Durable: record, Physical: physical}
	if receipt.RequestKind == "typed-v1" {
		request := manualNotebookRequestFromRecord(record)
		receipt.Request = &request
	}
	return receipt
}
