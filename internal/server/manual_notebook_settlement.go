package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) observeManualNotebookExecution(ctx context.Context, access workspace.KernelFrameAccess, record workspace.ManualKernelExecution, handle *kernelruntime.ExecutionHandle) {
	startedCh, doneCh := handle.Started(), handle.Done()
	var started *kernelruntime.ExecutionStarted
	var outcome kernelruntime.ExecutionOutcome
	ctxDone := ctx.Done()
	var shutdownTimer *time.Timer
	var shutdownDeadline <-chan time.Time
	defer func() {
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
	}()
	for doneCh != nil {
		select {
		case event, ok := <-startedCh:
			startedCh = nil
			if ok {
				started = &event
				persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				current, found, err := s.workspaceStore.GetManualKernelExecution(persistCtx, access, record.ExecutionID)
				if err == nil && found {
					_, err = s.workspaceStore.MarkManualKernelExecutionStarted(persistCtx, current, event.StartedAt)
				}
				cancel()
				if err != nil {
					log.Printf("manual kernel start persistence pending execution=%s: %v", record.ExecutionID, err)
				}
			}
		case result, ok := <-doneCh:
			doneCh = nil
			if !ok {
				s.persistManualNotebookUnknown(ctx, access, record.ExecutionID, "terminal_channel_closed_without_receipt")
				return
			}
			outcome = result
			// Started and Done may both be ready. A buffered real acknowledgement
			// must not be mistaken for a prestart failure because select chose Done.
			if startedCh != nil {
				if event, ok := <-startedCh; ok {
					started = &event
				}
				startedCh = nil
			}
		case <-ctxDone:
			ctxDone = nil
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_, err := s.interruptManualNotebookExecution(cancelCtx, access, record.ExecutionID)
			cancel()
			if err != nil {
				log.Printf("manual kernel shutdown cancellation pending execution=%s: %v", record.ExecutionID, err)
			}
			shutdownTimer = time.NewTimer(5 * time.Second)
			shutdownDeadline = shutdownTimer.C
		case <-shutdownDeadline:
			s.persistManualNotebookUnknown(ctx, access, record.ExecutionID, "shutdown_terminal_receipt_unavailable")
			return
		}
	}
	prestartRejected := started == nil && outcome.Response.ID == record.ExecutionID && manualNotebookPrestartDiagnostic(record, outcome) != "" && !outcome.TimedOut
	if !outcome.Dequeued && (!prestartRejected && started == nil || outcome.Response.ID != record.ExecutionID) {
		s.persistManualNotebookUnknown(ctx, access, record.ExecutionID, "worker_start_or_result_unconfirmed")
		// Unknown is durable uncertainty, not a physical-stop receipt. Keep
		// the handle's persistence reservation until explicit reconciliation.
		return
	}
	s.settleManualNotebookExecution(ctx, access, record, handle, manualNotebookCompletion(record, started, outcome), false)
}

// allowAbsent is used only after a rejected admission: the gate has proved no
// worker IO, while a commit error may have hidden whether the receipt exists.
func (s *Server) settleManualNotebookExecution(ctx context.Context, access workspace.KernelFrameAccess, record workspace.ManualKernelExecution, handle *kernelruntime.ExecutionHandle, completion workspace.ManualKernelCompletionInput, allowAbsent bool) {
	var attempt uint64
	for {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		current, found, err := s.workspaceStore.GetManualKernelExecution(persistCtx, access, record.ExecutionID)
		if err == nil && !found && allowAbsent {
			cancel()
			handle.AcknowledgePersistence()
			return
		}
		if err == nil && !found {
			err = workspace.ErrManualKernelExecutionStale
		}
		if err == nil && (current.ManualKernelBinding != record.ManualKernelBinding || current.Request != record.Request || current.RequestSHA256 != record.RequestSHA256) {
			if allowAbsent {
				cancel()
				handle.AcknowledgePersistence()
				return
			}
			err = workspace.ErrManualKernelExecutionConflict
		}
		if err == nil {
			completion.Expected = current
			_, err = s.workspaceStore.CompleteManualKernelExecution(persistCtx, completion)
		}
		// Done has supplied a terminal receipt. Release its in-memory handle
		// only if SQL proves the original frame/root incarnation is gone.
		// Owner mismatch or a missing lifecycle row alone is not that proof.
		retired := false
		if err != nil {
			retired, _ = s.workspaceStore.ManualKernelExecutionScopeRetired(persistCtx, record)
		}
		cancel()
		if err == nil {
			handle.AcknowledgePersistence()
			return
		}
		if retired {
			handle.AcknowledgePersistence()
			return
		}
		if logKernelSettlementAdmissionAttempt(attempt) {
			log.Printf("manual kernel terminal persistence pending execution=%s attempt=%d: %v", record.ExecutionID, attempt+1, err)
		}
		if errors.Is(err, workspace.ErrManualKernelExecutionConflict) || errors.Is(err, workspace.ErrManualKernelExecutionInvalid) || errors.Is(err, workspace.ErrWorkspaceStoreClosed) || ctx.Err() != nil || s.isDraining() {
			return
		}
		if err := waitKernelSettlementAdmissionRetry(ctx, attempt); err != nil {
			return
		}
		if attempt != ^uint64(0) {
			attempt++
		}
	}
}

func (s *Server) persistManualNotebookUnknown(ctx context.Context, access workspace.KernelFrameAccess, executionID, reason string) bool {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	record, found, err := s.workspaceStore.GetManualKernelExecution(persistCtx, access, executionID)
	if err == nil && !found {
		err = workspace.ErrManualKernelExecutionStale
	}
	if err == nil {
		_, err = s.workspaceStore.MarkManualKernelExecutionUnknown(persistCtx, record, reason)
	}
	if err != nil {
		log.Printf("manual kernel unknown outcome persistence pending execution=%s: %v", executionID, err)
	}
	return err == nil
}

func manualNotebookCompletion(record workspace.ManualKernelExecution, started *kernelruntime.ExecutionStarted, outcome kernelruntime.ExecutionOutcome) workspace.ManualKernelCompletionInput {
	input := workspace.ManualKernelCompletionInput{Expected: record, FinishedAt: outcome.FinishedAt}
	if outcome.Dequeued || started == nil {
		input.Disposition = "cancelled_before_start"
		diagnostic := manualNotebookPrestartDiagnostic(record, outcome)
		if outcome.Err != nil || diagnostic != "" {
			input.Disposition = "rejected_before_start"
			stderr := outcome.Response.Stderr
			for _, message := range []string{diagnostic, manualNotebookErrorText(outcome.Err)} {
				if message != "" {
					if stderr != "" {
						stderr += "\n"
					}
					stderr += message
				}
			}
			input.Rejection = &workspace.ManualKernelRejection{Stdout: outcome.Response.Stdout, Stderr: stderr}
		}
		return input
	}
	exitStatus := "ok"
	input.Disposition = "completed"
	stderr := outcome.Response.Stderr
	if outcome.Response.Error != "" {
		if stderr != "" {
			stderr += "\n"
		}
		stderr += outcome.Response.Error
	}
	if outcome.Response.Interrupted || outcome.TimedOut {
		exitStatus = "cancelled"
		input.Disposition = "cancelled"
	} else if outcome.Err != nil || outcome.Response.Error != "" {
		exitStatus = "error"
		input.Disposition = "failed"
		if outcome.Err != nil {
			if stderr != "" {
				stderr += "\n"
			}
			stderr += outcome.Err.Error()
		}
	}
	input.Log = &workspace.ExecutionLogRecord{ID: record.ExecutionID, FrameID: record.FrameID, CellIndex: outcome.CellIndex, KernelID: record.KernelID, KernelKind: record.KernelKind, CondaEnv: record.Request.Environment, Language: record.Request.Language, Source: record.Request.Source, Stdout: outcome.Response.Stdout, Stderr: stderr, ExitStatus: exitStatus, Origin: "user", ExecutedAt: started.StartedAt, ErrorLine: kernelErrorLine(outcome.Response.Trace)}
	return input
}

func manualNotebookErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func manualNotebookPrestartDiagnostic(record workspace.ManualKernelExecution, outcome kernelruntime.ExecutionOutcome) string {
	if outcome.Response.Error != "" {
		return outcome.Response.Error
	}
	// The worker's syntax/import refusal is a typed preflight envelope with
	// error=null. Reuse its canonical validator, including no execution effects.
	preflight, err := agentKernelPythonCodePreflight(kernelruntime.SessionSpec{Language: record.Request.Language, KernelKind: record.KernelKind}, outcome, "ok")
	if err != nil || preflight == nil {
		return ""
	}
	return strings.TrimSpace(stringValue(preflight["message"]) + "\n" + stringValue(preflight["recovery"]))
}
