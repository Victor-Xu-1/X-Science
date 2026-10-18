package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) submitKernelTerminalExecution(access workspace.KernelFrameAccess, frameID, language, environment, code string) (string, string, error) {
	if frameID != access.Frame.ID {
		return "", "", workspace.ErrManualKernelExecutionConflict
	}
	record, err := s.submitManualNotebookExecution(context.Background(), access, workspace.ManualKernelRequest{
		RequestID: uuid.NewString(), Source: code, Language: language, Environment: environment, TimeoutMillis: userTerminalCellTimeout.Milliseconds(),
	})
	if err != nil {
		return "", "", err
	}
	return record.ExecutionID, "user-" + record.ExecutionID, nil
}

// Both legacy transports and typed HTTP submission use this controller. Only
// the typed contract supplies a frozen RequestID; WS request_id is correlation.
func (s *Server) submitManualNotebookExecution(ctx context.Context, access workspace.KernelFrameAccess, request workspace.ManualKernelRequest) (workspace.ManualKernelExecution, error) {
	if s == nil || s.workspaceStore == nil || s.kernelManager == nil {
		return workspace.ManualKernelExecution{}, errors.New("verified kernel runtime is unavailable")
	}
	if existing, found, err := s.workspaceStore.LookupManualKernelExecution(ctx, access, request); err != nil || found {
		return existing, err
	}
	execID := workspace.ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID)
	observerID := "manual:" + execID
	observerCtx, observerCancel, reserved := s.reserveDetachedKernelObserver(context.Background(), observerID)
	if !reserved {
		return workspace.ManualKernelExecution{}, errors.New("manual execution admission is already pending or runtime is draining")
	}
	observerOwned := true
	defer func() {
		if observerOwned {
			s.releaseDetachedKernelObserver(observerID, observerCancel)
		}
	}()
	gate := make(chan error, 1)
	handle, err := s.kernelManager.Submit(kernelruntime.SubmitRequest{
		KernelID: request.KernelID, ExpectedGeneration: request.ExpectedKernelGeneration,
		OwnerID: access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID,
		FrameIncarnationID: access.Frame.IncarnationID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		Language: request.Language, Environment: request.Environment, ExecID: execID, ToolUseID: "user-" + execID,
		Code: request.Source, Origin: "user", Timeout: time.Duration(request.TimeoutMillis) * time.Millisecond,
		StartAuthorization: gate, PersistenceRequired: true,
	})
	if err != nil {
		if strings.HasPrefix(strings.ToLower(err.Error()), "no running ") {
			return workspace.ManualKernelExecution{}, fmt.Errorf("No running %s kernel for environment '%s' on this frame — the terminal attaches to live kernels only. Ask the agent to run a cell first (or pick another kernel).", request.Language, request.Environment)
		}
		return workspace.ManualKernelExecution{}, err
	}
	reject := func(cause error) error {
		gate <- cause
		close(gate)
		outcome := <-handle.Done()
		if !outcome.Dequeued {
			return errors.Join(cause, errors.New("manual admission did not reject before worker IO"))
		}
		handle.AcknowledgePersistence()
		return cause
	}
	identity := handle.Identity()
	if identity.Source != request.Source || identity.Language != request.Language || identity.Environment != request.Environment || identity.Origin != "user" || identity.Timeout != time.Duration(request.TimeoutMillis)*time.Millisecond {
		return workspace.ManualKernelExecution{}, reject(workspace.ErrManualKernelExecutionConflict)
	}
	binding := workspace.ManualKernelBinding{ExecutionID: identity.ExecutionID, OwnerID: identity.OwnerID, ProjectID: identity.ProjectID, RootFrameID: identity.RootFrameID, RootIncarnationID: identity.RootFrameIncarnationID, FrameID: identity.FrameID, FrameIncarnationID: identity.FrameIncarnationID, KernelID: identity.KernelID, KernelGeneration: identity.KernelGeneration, KernelKind: identity.KernelKind, ServerBootID: s.kernelOperationBootID}
	var record workspace.ManualKernelExecution
	created := false
	admissionAttempted := false
	err = s.withKernelHostGrantAdmission(access.UserID, func() error {
		var admissionErr error
		admissionAttempted = true
		record, created, admissionErr = s.workspaceStore.AdmitManualKernelExecution(ctx, workspace.AdmitManualKernelExecutionInput{Binding: binding, Request: request})
		if admissionErr == nil && created {
			observerOwned = false
			s.launchReservedAgentKernelExecutionObserver(observerCtx, observerID, observerCancel, func(ctx context.Context) { s.observeManualNotebookExecution(ctx, access, record, handle) })
			gate <- nil
			close(gate)
		}
		return admissionErr
	})
	if err != nil {
		if !admissionAttempted || record.ExecutionID == "" {
			return workspace.ManualKernelExecution{}, reject(err)
		}
		gate <- err
		close(gate)
		outcome := <-handle.Done()
		if !outcome.Dequeued {
			return workspace.ManualKernelExecution{}, errors.Join(err, errors.New("manual admission did not reject before worker IO"))
		}
		observerOwned = false
		s.launchReservedAgentKernelExecutionObserver(observerCtx, observerID, observerCancel, func(ctx context.Context) {
			s.settleManualNotebookExecution(ctx, access, record, handle, manualNotebookCompletion(record, nil, outcome), true)
		})
		return workspace.ManualKernelExecution{}, err
	}
	if !created {
		denied := errors.New("existing manual execution already owns this request")
		if err := reject(denied); err != denied {
			return workspace.ManualKernelExecution{}, err
		}
		return record, nil
	}
	return record, nil
}

func (s *Server) interruptManualNotebookExecution(ctx context.Context, access workspace.KernelFrameAccess, executionID string) (kernelruntime.InterruptResult, error) {
	record, found, err := s.workspaceStore.GetManualKernelExecution(ctx, access, executionID)
	if err != nil {
		return kernelruntime.InterruptResult{}, err
	}
	if found {
		if record.State == "terminal" {
			return kernelruntime.InterruptResult{Reason: "cell is already settled"}, nil
		}
		if record.State == "outcome_unknown" {
			return kernelruntime.InterruptResult{Reason: "execution outcome is unknown"}, nil
		}
		if _, err := s.workspaceStore.RequestManualKernelExecutionCancellation(ctx, record); err != nil {
			return kernelruntime.InterruptResult{}, err
		}
	}
	// Pre-upgrade executions retain exact-id interrupt compatibility, never a
	// latest-cell fallback or a fabricated durable cancellation receipt.
	return s.kernelManager.InterruptSession(access.Frame.ID, access.Frame.IncarnationID, access.RootFrameIncarnationID, executionID), nil
}
