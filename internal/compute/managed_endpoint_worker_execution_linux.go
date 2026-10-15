//go:build linux

package compute

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (worker *managedEndpointWorker) execute(parent context.Context, action ManagedEndpointAction, values map[string]string) {
	worker.execution.Lock()
	defer worker.execution.Unlock()
	deadline := 2 * time.Minute
	if action.Ref.Kind == "stop" {
		deadline = time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	started := time.Now()
	receipt := ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true}
	worker.mu.Lock()
	instance := worker.instance
	worker.mu.Unlock()
	if instance.Registration.Owner.Kind == "native-unit" {
		receipt.Identity = worker.identity
		receipt.OwnerVerified = true
	}
	worker.output.mu.Lock()
	worker.output.prefix = cappedBuffer{}
	worker.output.mu.Unlock()
	var docker *managedEndpointDocker
	if instance.Registration.Owner.Kind == "docker" {
		var err error
		docker, err = newManagedEndpointDocker()
		if err != nil {
			receipt.Failure = err.Error()
		} else if action.Ref.Kind == "stop" {
			if proof, proofErr := docker.observeStopped(ctx, instance); proofErr == nil {
				receipt.NoScript = true
				receipt.Identity = proof.Identity
				receipt.OwnerVerified = true
				receipt.Stopped = true
				receipt.StoppedProof = proof
			}
		}
		if receipt.Failure == "" && !receipt.NoScript {
			if err := docker.validateNamedOwner(ctx, instance, action.Ref.Kind == "start"); err != nil {
				receipt.Failure = err.Error()
			}
			if receipt.Failure == "" && action.Ref.Kind == "start" && instance.Identity.Kind == "docker" {
				_, state, err := docker.inspect(ctx, instance)
				if err == nil {
					err = validateManagedEndpointDockerLogging(state)
				}
				if err != nil {
					receipt.Failure = err.Error()
				}
			}
		}
	}
	if receipt.Failure != "" || receipt.NoScript {
		// Preflight failure is not a script exit. Keep that distinction visible.
	} else if action.Ref.Kind == "start" && managedEndpointInitialAdoption(instance, action.Ref) {
		receipt.NoScript = true
	} else {
		script := instance.Registration.StartScript
		if action.Ref.Kind == "stop" {
			script = instance.Registration.StopScript
		}
		environment, err := managedEndpointActionEnvironment(instance, values)
		if err != nil {
			receipt.Failure = err.Error()
		} else {
			exit := runManagedEndpointOwnedAction(ctx, script, environment, worker.writer)
			receipt.ProcessExited, receipt.ExitCode = exit.Exited, exit.Code
			if exit.Err != nil {
				receipt.Failure = exit.Err.Error()
			}
		}
	}
	if receipt.Failure == "" {
		if action.Ref.Kind == "start" {
			worker.verifyReady(ctx, instance, &receipt)
		} else if instance.Registration.Owner.Kind == "docker" {
			runtime, err := newManagedEndpointDocker()
			if err == nil {
				receipt.StoppedProof, err = runtime.observeStopped(ctx, instance)
			}
			if err != nil {
				receipt.Failure = err.Error()
			} else {
				receipt.Identity = receipt.StoppedProof.Identity
				receipt.OwnerVerified = true
				receipt.Stopped = true
			}
		}
	}
	if receipt.Failure != "" && action.Ref.Kind == "start" && docker != nil && !managedEndpointInitialAdoption(instance, action.Ref) {
		worker.mu.Lock()
		var journalErr error
		if worker.action.Ref == action.Ref {
			worker.action.CleanupPending = true
			journalErr = writeManagedEndpointPrivateJSON(worker.journalPath, worker.action)
			if journalErr == nil {
				worker.cleanupRunning = true
			}
		}
		worker.mu.Unlock()
		if journalErr == nil {
			worker.cleanupDocker(instance, &receipt, values)
		} else {
			receipt.CleanupFailure = "cleanup admission was not persisted: " + journalErr.Error()
		}
	}
	text, sequence, dropped, closed, outputErr := worker.output.snapshot()
	receipt.Transcript, receipt.OutputSequence, receipt.OutputDroppedBytes, receipt.OutputClosed = text, sequence, dropped, closed
	if outputErr != nil {
		receipt.Failure = errors.Join(errors.New(receipt.Failure), fmt.Errorf("managed endpoint output persistence: %w", outputErr)).Error()
	}
	if worker.recoveredOutput {
		receipt.OutputWarning = "The output reader was reconstructed after worker loss; earlier output continuity is unverified."
	}
	receipt.Duration = time.Since(started)
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.action.Ref != action.Ref {
		return
	}
	if worker.action.CancelRequested && receipt.Failure == "" {
		receipt.Failure = "managed endpoint action was cancelled"
	}
	if receipt.OwnerVerified && receipt.Identity != (ManagedEndpointRuntimeIdentity{}) {
		worker.instance.Identity = receipt.Identity
	}
	worker.action.Receipt = &receipt
	worker.action.CleanupPending = false
	worker.cleanupRunning = false
	if err := writeManagedEndpointPrivateJSON(worker.journalPath, worker.action); err != nil {
		receipt.Failure = errors.Join(errors.New(receipt.Failure), fmt.Errorf("persist managed endpoint result: %w", err)).Error()
		worker.action.Receipt = &receipt
	}
	worker.cancel = nil
}

func managedEndpointInitialAdoption(instance ManagedEndpointOwnerInstance, ref ManagedEndpointActionRef) bool {
	return instance.Registration.Owner.Adopt != nil && ref == instance.InitialAction
}

// Cleanup is a separately recorded finite approved action, not a second start.
// Missing acknowledgement after this journal is uncertainty, never permission
// to execute the cleanup bytes again after worker loss.
func (worker *managedEndpointWorker) cleanupDocker(instance ManagedEndpointOwnerInstance, receipt *ManagedEndpointOwnerReceipt, values map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	docker, err := newManagedEndpointDocker()
	if err != nil {
		receipt.CleanupFailure = err.Error()
		return
	}
	identity, _, err := docker.inspect(ctx, instance)
	if err != nil {
		receipt.CleanupFailure = err.Error()
		return
	}
	instance.Identity = identity
	receipt.Identity = identity
	receipt.OwnerVerified = true
	if proof, err := docker.observeStopped(ctx, instance); err == nil {
		receipt.Stopped = true
		receipt.StoppedProof = proof
		return
	}
	if err := docker.validateNamedOwner(ctx, instance, false); err != nil {
		receipt.CleanupFailure = err.Error()
		return
	}
	environment, err := managedEndpointActionEnvironment(instance, values)
	if err != nil {
		receipt.CleanupFailure = err.Error()
		return
	}
	exit := runManagedEndpointOwnedAction(ctx, instance.Registration.StopScript, environment, worker.writer)
	receipt.CleanupProcessExited, receipt.CleanupExitCode = exit.Exited, exit.Code
	if exit.Err != nil {
		receipt.CleanupFailure = exit.Err.Error()
	}
	proof, proofErr := docker.observeStopped(ctx, instance)
	if proofErr != nil {
		receipt.CleanupFailure = errors.Join(exit.Err, proofErr).Error()
		return
	}
	receipt.Stopped, receipt.StoppedProof = true, proof
}

func (worker *managedEndpointWorker) finishDockerCancellation(ref ManagedEndpointActionRef, receipt ManagedEndpointOwnerReceipt, values map[string]string) {
	worker.execution.Lock()
	defer worker.execution.Unlock()
	worker.mu.Lock()
	instance := worker.instance
	worker.mu.Unlock()
	worker.cleanupDocker(instance, &receipt, values)
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.action.Ref != ref {
		return
	}
	worker.action.Receipt = &receipt
	worker.action.CleanupPending = false
	worker.cleanupRunning = false
	if err := writeManagedEndpointPrivateJSON(worker.journalPath, worker.action); err != nil {
		receipt.CleanupFailure = errors.Join(errors.New(receipt.CleanupFailure), err).Error()
		worker.action.Receipt = &receipt
	}
}
