package kernel

import "time"

// ExecutionIdentity is the immutable registration selected by Submit. It
// contains no mutable worker pointer and never selects a newer kernel.
type ExecutionIdentity struct {
	ExecutionID, ToolUseID, KernelID                  string
	OwnerID, ProjectID, FrameID, RootFrameID          string
	FrameIncarnationID, RootFrameIncarnationID        string
	KernelKind, Language, Environment, Source, Origin string
	KernelGeneration                                  uint64
	Timeout                                           time.Duration
}

func executionIdentity(spec SessionSpec, input SubmitRequest, generation uint64) ExecutionIdentity {
	return ExecutionIdentity{
		ExecutionID: input.ExecID, ToolUseID: input.ToolUseID, KernelID: spec.KernelID,
		OwnerID: spec.OwnerID, ProjectID: spec.ProjectID, FrameID: spec.FrameID, RootFrameID: spec.RootFrameID,
		FrameIncarnationID: spec.FrameIncarnationID, RootFrameIncarnationID: spec.RootFrameIncarnationID,
		KernelKind: spec.KernelKind, Language: spec.Language, Environment: spec.Environment,
		Source: input.Code, Origin: input.Origin, KernelGeneration: generation, Timeout: input.Timeout,
	}
}

func (h *ExecutionHandle) Identity() ExecutionIdentity {
	if h == nil || h.execution == nil {
		return ExecutionIdentity{}
	}
	return h.execution.identity
}
