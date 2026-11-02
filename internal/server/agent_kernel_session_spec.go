package server

import (
	"context"
	"errors"

	kernelruntime "x-science/internal/kernel"
	"x-science/internal/kernelcontract"
	workspace "x-science/internal/persistence/workspace"
)

// agentKernelSessionRuntime contains the execution attributes that are part of
// a stable kernel identity plus the public tool identity used to derive its
// mutable authority. Keep authority assembly centralized: callers that
// pre-create or resume a stable session must present the same complete mounts,
// protected paths, and egress policy as the eventual execution path.
type agentKernelSessionRuntime struct {
	PublicName        string
	Kind              string
	Language          string
	Environment       string
	RuntimeGeneration string
	ExcludePackID     string
}

type agentKernelExecutionSessionSpecInput struct {
	Runtime   agentKernelSessionRuntime
	Input     map[string]any
	TaskOwned bool
	Operation *workspace.KernelLocalOperation
}

// prepareAgentKernelExecutionSessionSpec completes the shared inline/detached
// session identity before either execution backend can admit work.
func (s *Server) prepareAgentKernelExecutionSessionSpec(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	workspaceDir string,
	input agentKernelExecutionSessionSpecInput,
) (kernelruntime.SessionSpec, error) {
	runtime := input.Runtime
	runtime.ExcludePackID = ""
	if engine, found := s.canonicalManagedExecutionPack(runtime.PublicName, input.Input); found {
		runtime.ExcludePackID = engine.ExecutionPack.ID
	}
	finishSpec := agentKernelPrepareTimingFromContext(ctx).begin(kernelPrepareSpec)
	spec, err := s.agentKernelSessionSpec(ctx, access, workspaceDir, runtime)
	finishSpec()
	if err != nil {
		return spec, err
	}
	if input.TaskOwned {
		// Application execution packs own their typed resource contract.
		if input.Operation == nil {
			return spec, errors.New("task-owned runtime durable operation authority is unavailable")
		}
		spec.KernelID = softwareRuntimeKernelID(input.Operation.OperationID)
	}
	spec.ResourceMemoryBytes, err = kernelcontract.MemoryBudgetBytes(input.Input)
	return spec, err
}

func (s *Server) agentKernelSessionSpec(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	workspaceDir string,
	runtime agentKernelSessionRuntime,
) (kernelruntime.SessionSpec, error) {
	protectedPaths, err := s.agentKernelProtectedPaths()
	if err != nil {
		return kernelruntime.SessionSpec{}, err
	}
	mounts, err := s.agentKernelConfinementMounts(access.UserID, workspaceDir, protectedPaths)
	if err != nil {
		return kernelruntime.SessionSpec{}, err
	}
	immutableMounts, err := s.agentKernelWorkspaceImmutableMounts(
		ctx, access, workspaceDir, runtime.ExcludePackID,
	)
	if err != nil {
		return kernelruntime.SessionSpec{}, err
	}
	mounts = append(mounts, immutableMounts...)
	egressAllowed, egressDenied, caBundle, upstreamProxy, err := s.agentKernelEgressPolicy(
		runtime.PublicName, access.Frame.ID,
	)
	if err != nil {
		return kernelruntime.SessionSpec{}, err
	}
	agentName, definitionID := access.Frame.AgentName, ""
	snapshot, err := s.claimedSessionSpecialistForFrame(ctx, access.UserID, access.Frame.ID)
	if err != nil {
		return kernelruntime.SessionSpec{}, err
	}
	if snapshot != nil {
		agentName, definitionID = snapshot.AgentName, snapshot.ID
	}
	var startup kernelruntime.SessionSpec
	if runtime.Kind == "bash" && runtime.Language == "python" && s.kernelManager != nil {
		startup.WorkerStartupPolicy, startup.SupervisorRuntimeGeneration = s.kernelManager.CanonicalBashStartup()
	}
	return kernelruntime.SessionSpec{
		WorkerStartupPolicy: startup.WorkerStartupPolicy, SupervisorRuntimeGeneration: startup.SupervisorRuntimeGeneration,
		OwnerID: access.UserID, ProjectID: access.Frame.ProjectID,
		FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		AgentName: agentName, AgentDefinitionID: definitionID, DelegateName: access.DelegateName,
		KernelKind: runtime.Kind, Language: runtime.Language,
		Environment: runtime.Environment, RuntimeGeneration: runtime.RuntimeGeneration,
		WorkspaceDir: workspaceDir, Mounts: mounts, ProtectedPaths: protectedPaths,
		EgressAllowedDomains: egressAllowed, EgressDeniedDomains: egressDenied,
		CABundle: caBundle, UpstreamProxy: upstreamProxy,
	}, nil
}
