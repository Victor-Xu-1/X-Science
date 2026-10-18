package server

import (
	"net/http"
	"strings"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

type manualNotebookScope struct {
	Schema             string `json:"schema"`
	FrameID            string `json:"frame_id"`
	RootFrameID        string `json:"root_frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	RootIncarnationID  string `json:"root_frame_incarnation_id"`
}

func manualNotebookInventoryScope(r *http.Request, access workspace.KernelFrameAccess) *manualNotebookScope {
	actor := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if actor == "" || actor != access.UserID || access.IsHidden {
		return nil
	}
	return &manualNotebookScope{Schema: "x-science.manual-kernel-scope.v1", FrameID: access.Frame.ID,
		RootFrameID: access.Frame.RootFrameID, FrameIncarnationID: access.Frame.IncarnationID,
		RootIncarnationID: access.RootFrameIncarnationID}
}

type manualNotebookTarget struct {
	Schema             string `json:"schema"`
	FrameID            string `json:"frame_id"`
	RootFrameID        string `json:"root_frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	RootIncarnationID  string `json:"root_frame_incarnation_id"`
	KernelID           string `json:"kernel_id"`
	KernelGeneration   uint64 `json:"kernel_generation"`
	Language           string `json:"language"`
	Environment        string `json:"environment"`
}

type manualNotebookKernelProjection struct {
	kernelruntime.SessionKernel
	ManualExecutionTarget *manualNotebookTarget `json:"manual_execution_target,omitempty"`
}

func (s *Server) manualNotebookKernelProjections(inventory authorizedKernelInventory, userID, rootID string) []manualNotebookKernelProjection {
	result := make([]manualNotebookKernelProjection, 0, len(inventory.Kernels))
	for _, kernel := range inventory.Kernels {
		if rootID != "" && kernel.RootFrameID != rootID {
			continue
		}
		projection := manualNotebookKernelProjection{SessionKernel: kernel}
		_, detached := inventory.Detached[kernel.KernelID]
		if !detached && s.kernelManager != nil {
			target, found := s.kernelManager.LocalExecutionTarget(kernel.KernelID)
			if found && target.OwnerID == userID && target.ProjectID == kernel.ProjectID &&
				target.FrameID == kernel.FrameID && target.RootFrameID == kernel.RootFrameID &&
				target.FrameIncarnationID == kernel.FrameIncarnationID && target.RootFrameIncarnationID == kernel.RootFrameIncarnationID &&
				target.Generation == kernel.RegistrationGeneration && target.Generation <= 9007199254740991 && target.KernelKind == "analysis" &&
				target.Language == kernel.Language && target.Environment == kernel.Environment &&
				(target.Language == "python" || target.Language == "r") {
				projection.ManualExecutionTarget = &manualNotebookTarget{Schema: "x-science.manual-kernel-target.v1",
					FrameID: target.FrameID, RootFrameID: target.RootFrameID,
					FrameIncarnationID: target.FrameIncarnationID, RootIncarnationID: target.RootFrameIncarnationID,
					KernelID: target.KernelID, KernelGeneration: target.Generation, Language: target.Language, Environment: target.Environment}
			}
		}
		result = append(result, projection)
	}
	return result
}

func (s *Server) validateManualNotebookLiveTarget(access workspace.KernelFrameAccess, expected manualNotebookExpectedTarget) error {
	if s.kernelManager == nil {
		return &manualNotebookAPIError{http.StatusServiceUnavailable, "MANUAL_EXECUTION_RUNTIME_UNAVAILABLE", "Kernel runtime is unavailable"}
	}
	target, found := s.kernelManager.LocalExecutionTarget(expected.KernelID)
	if !found || target.OwnerID != access.UserID || target.ProjectID != access.Frame.ProjectID ||
		target.FrameID != access.Frame.ID || target.RootFrameID != access.Frame.RootFrameID ||
		target.FrameIncarnationID != expected.FrameIncarnationID || target.RootFrameIncarnationID != expected.RootIncarnationID ||
		target.Generation != expected.KernelGeneration || target.KernelKind != "analysis" ||
		target.Language != expected.Language || target.Environment != expected.Environment {
		return &manualNotebookAPIError{http.StatusConflict, "MANUAL_EXECUTION_TARGET_CHANGED", "The frozen local kernel target is no longer available"}
	}
	return nil
}
