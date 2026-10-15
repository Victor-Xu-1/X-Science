//go:build linux

package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

func (worker *managedEndpointWorker) serveConnection(connection net.Conn) {
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	decoder := json.NewDecoder(io.LimitReader(connection, 256<<10+1))
	decoder.DisallowUnknownFields()
	var request managedEndpointWorkerRequest
	if err := decoder.Decode(&request); err != nil {
		return
	}
	response := managedEndpointWorkerResponse{Version: ManagedEndpointOwnerProtocol, InstanceID: worker.instance.ID, WorkerIdentity: worker.identity}
	if request.Version != ManagedEndpointOwnerProtocol || request.InstanceID != worker.instance.ID || request.Action.Ref.InstanceID != worker.instance.ID {
		response.Failure = "managed endpoint request identity does not match"
		response.FailureCode = "invalid_authority"
	} else if err := request.Action.Ref.Validate(); err != nil {
		response.Failure = err.Error()
		response.FailureCode = "invalid_authority"
	} else {
		var operationErr error
		switch request.Method {
		case "submit":
			operationErr = worker.submit(request)
		case "cancel":
			operationErr = worker.requestCancel(request.Action, request.Environment)
		case "observe":
			worker.mu.Lock()
			if worker.action.Ref == request.Action.Ref && worker.action.Accepted && (worker.action.Receipt == nil && worker.cancel == nil || worker.action.CleanupPending && !worker.cleanupRunning) {
				operationErr = ErrManagedEndpointOutcomeUnknown
			}
			worker.mu.Unlock()
		default:
			operationErr = errors.New("unknown managed endpoint worker method")
		}
		if operationErr != nil {
			response.Failure = operationErr.Error()
			response.FailureCode = "operation_failed"
			if errors.Is(operationErr, ErrManagedEndpointStaleAction) {
				response.FailureCode = "stale_action"
			}
			if errors.Is(operationErr, ErrManagedEndpointOutcomeUnknown) {
				response.FailureCode = "outcome_unknown"
			}
		}
		worker.mu.Lock()
		if worker.action.Ref == request.Action.Ref {
			response.Action = worker.action
		} else if response.Failure == "" {
			response.Failure = ErrManagedEndpointStaleAction.Error()
			response.FailureCode = "stale_action"
		}
		worker.mu.Unlock()
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func (worker *managedEndpointWorker) submit(request managedEndpointWorkerRequest) error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	ref := request.Action.Ref
	if ref.Generation < worker.action.Ref.Generation {
		return ErrManagedEndpointStaleAction
	}
	if ref.Generation == worker.action.Ref.Generation && worker.action.Ref.InstanceID != "" {
		if ref != worker.action.Ref {
			return ErrManagedEndpointStaleAction
		}
		// A persisted acceptance without a result is uncertainty, not permission
		// to rerun a high-cost or external side effect after worker loss.
		if worker.cancel == nil && worker.action.Receipt == nil {
			return ErrManagedEndpointOutcomeUnknown
		}
		return nil
	}
	if request.Action.CancelRequested {
		return errors.New("cancelled action cannot start")
	}
	if _, err := managedEndpointActionEnvironment(worker.instance, request.Environment); err != nil {
		return err
	}
	admissionCtx, stopAdmission := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopAdmission()
	if worker.cancel != nil {
		_, prior, err := worker.store.ReadManagedEndpointAction(admissionCtx, worker.action.Ref)
		if err != nil {
			return err
		}
		if !prior.CancelRequested {
			return ErrManagedEndpointActionPending
		}
	}
	accepted, admissionErr := worker.store.AcceptManagedEndpointAction(admissionCtx, ref, worker.identity)
	if admissionErr != nil {
		return admissionErr
	}
	if !accepted {
		return ErrManagedEndpointOutcomeUnknown
	}
	// No caller-controlled higher ref may mutate the current execution before
	// durable admission. Superseding a still-running action additionally requires
	// its own previously committed cancellation authority, not just a newer ref.
	if worker.cancel != nil {
		worker.cancel()
	}
	action := ManagedEndpointAction{Ref: ref, Accepted: true}
	worker.action = action
	worker.cancel = nil
	if err := writeManagedEndpointPrivateJSON(worker.journalPath, action); err != nil {
		return fmt.Errorf("persist admitted endpoint action; outcome unknown: %w", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	worker.action, worker.cancel = action, cancel
	go worker.execute(runCtx, action, request.Environment)
	return nil
}

func (worker *managedEndpointWorker) requestCancel(action ManagedEndpointAction, values map[string]string) error {
	gateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, canonical, err := worker.store.ReadManagedEndpointAction(gateCtx, action.Ref)
	if err != nil {
		return err
	}
	if !canonical.CancelRequested {
		return ErrManagedEndpointStaleAction
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if action.Ref != worker.action.Ref || !action.CancelRequested {
		return ErrManagedEndpointStaleAction
	}
	worker.action.CancelRequested = true
	if err := writeManagedEndpointPrivateJSON(worker.journalPath, worker.action); err != nil {
		return err
	}
	if worker.cancel != nil {
		worker.cancel()
	}
	if worker.action.Receipt != nil {
		receipt := *worker.action.Receipt
		receipt.Failure = "managed endpoint startup cancelled before live handoff"
		worker.action.Receipt = &receipt
		if worker.instance.Registration.Owner.Kind == "docker" && !managedEndpointInitialAdoption(worker.instance, action.Ref) && action.Ref.Kind == "start" {
			if worker.action.CleanupPending {
				return ErrManagedEndpointOutcomeUnknown
			}
			worker.action.CleanupPending = true
		}
		if err := writeManagedEndpointPrivateJSON(worker.journalPath, worker.action); err != nil {
			return err
		}
		if worker.action.CleanupPending {
			worker.cleanupRunning = true
			go worker.finishDockerCancellation(action.Ref, receipt, values)
		}
	}
	return nil
}
