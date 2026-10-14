package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/toolprogress"
)

func TestRecoveredKernelProgressPreservesArgumentsAndExecution(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	arguments := json.RawMessage(`{"command":"true","memory_budget_mb":0}`)
	call := agentruntime.ToolCall{ID: "original-call", Name: "bash", Arguments: json.RawMessage(`{"command":"true"}`)}
	progress := make(chan agentruntime.Event, 4)
	finished := make(chan error, 1)
	stopCause := errors.New("original request stopped")
	go func() {
		_, err := observeRecoveredKernelTool(ctx, call, arguments, func(executionCtx context.Context) (map[string]any, error) {
			toolprogress.Report(executionCtx, toolprogress.Update{Phase: "waiting_for_resources", Message: "Waiting for execution capacity", Indeterminate: true})
			<-executionCtx.Done()
			return nil, context.Cause(executionCtx)
		}, func(event agentruntime.Event) error {
			progress <- event
			return errors.New("transient publication failure")
		})
		finished <- err
	}()
	select {
	case event := <-progress:
		if event.Type != agentruntime.EventToolProgress || event.ToolCallID != call.ID || event.ToolName != call.Name || event.Arguments != string(arguments) || event.ProgressOrdinal != 1 || event.Progress == nil || event.Progress.Phase != "waiting_for_resources" {
			t.Fatalf("unexpected recovery observation: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery did not publish resource waiting before completion")
	}
	select {
	case err := <-finished:
		t.Fatalf("publication failure ended the execution: %v", err)
	default:
	}
	cancel(stopCause)
	select {
	case err := <-finished:
		if !errors.Is(err, stopCause) {
			t.Fatalf("cancellation cause=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery observer did not exit with its original request")
	}
}
