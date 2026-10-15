//go:build linux

package compute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

type ManagedEndpointSystemRuntime struct {
	root       string
	executable string
	unit       *managedEndpointUnit
	store      ManagedEndpointOwnerStore
}

func NewManagedEndpointSystemRuntime(root, executable string, store ManagedEndpointOwnerStore) (ManagedEndpointOwnerRuntime, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(executable) {
		return nil, errors.New("managed endpoint runtime requires absolute owned paths")
	}
	unit, err := newManagedEndpointUnit()
	if err != nil {
		return nil, err
	}
	if err := ensureManagedEndpointPrivateDirectory(root); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("managed endpoint runtime admission store is unavailable")
	}
	return &ManagedEndpointSystemRuntime{root: root, executable: executable, unit: unit, store: store}, nil
}

func (runtime *ManagedEndpointSystemRuntime) validate(instance ManagedEndpointOwnerInstance, action ManagedEndpointAction) error {
	if err := instance.Validate(); err != nil {
		return err
	}
	if instance.RuntimeRoot != runtime.root || action.Ref.InstanceID != instance.ID {
		return ErrManagedEndpointStaleAction
	}
	return action.Ref.Validate()
}

func (runtime *ManagedEndpointSystemRuntime) worker(ctx context.Context, instance ManagedEndpointOwnerInstance, launch, replacement bool) (managedEndpointUnitObservation, error) {
	// One budget covers physical observation, launch acknowledgement and the
	// nonmutating control handshake, including already/concurrently live units.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	observed, err := runtime.unit.observe(ctx, instance)
	if err != nil {
		return observed, err
	}
	if !observed.Absent && observed.State != "inactive" && observed.State != "failed" {
		if instance.WorkerIdentity != (ManagedEndpointRuntimeIdentity{}) && instance.WorkerIdentity != observed.Identity && !replacement {
			return observed, ErrManagedEndpointOwnershipUnknown
		}
		return runtime.awaitWorkerControl(ctx, instance, observed)
	}
	if !launch {
		return observed, ErrManagedEndpointOutcomeUnknown
	}
	// Existing native services are never moved or killed just because their
	// approved adoption descriptor cannot be connected to this private protocol.
	if instance.Registration.Owner.Kind == "native-unit" && instance.Registration.Owner.Adopt != nil {
		return observed, ErrManagedEndpointOwnershipUnknown
	}
	if instance.Identity.Kind == "native-unit" {
		return observed, ErrManagedEndpointOutcomeUnknown
	}
	if instance.WorkerIdentity != (ManagedEndpointRuntimeIdentity{}) && !replacement {
		return observed, ErrManagedEndpointOutcomeUnknown
	}
	if replacement {
		if instance.Registration.Owner.Kind != "docker" {
			return observed, ErrManagedEndpointOwnershipUnknown
		}
		docker, err := newManagedEndpointDocker()
		if err != nil {
			return observed, err
		}
		if _, _, err := docker.inspect(ctx, instance); err != nil {
			return observed, err
		}
	}
	socket, err := managedEndpointSocketPath(instance)
	if err != nil {
		return observed, err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return observed, ErrManagedEndpointCorruptOwner
		}
		// Only this positively absent, unpredictable instance's own stale socket.
		if err := os.Remove(socket); err != nil {
			return observed, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return observed, err
	}
	launchErr := runtime.unit.launch(ctx, instance, runtime.executable)
	if errors.Is(launchErr, ErrManagedEndpointCorruptOwner) || errors.Is(launchErr, ErrManagedEndpointOwnershipUnknown) || errors.Is(launchErr, os.ErrPermission) {
		return observed, launchErr
	}
	// A concurrent same-claim caller may have launched the exact same unit.
	// Observe it instead of interpreting an existing-unit error as a second job.
	for {
		observed, err = runtime.unit.observe(ctx, instance)
		if err != nil {
			return observed, errors.Join(launchErr, err)
		}
		if observed.Identity.PID > 0 {
			if instance.WorkerIdentity != (ManagedEndpointRuntimeIdentity{}) && instance.WorkerIdentity != observed.Identity && !replacement {
				return observed, ErrManagedEndpointOwnershipUnknown
			}
			return runtime.awaitWorkerControl(ctx, instance, observed)
		}
		select {
		case <-ctx.Done():
			return observed, errors.Join(launchErr, context.Cause(ctx))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (runtime *ManagedEndpointSystemRuntime) call(ctx context.Context, instance ManagedEndpointOwnerInstance, worker managedEndpointUnitObservation, method string, action ManagedEndpointAction, environment map[string]string) (result ManagedEndpointAction, err error) {
	defer func() {
		if err != nil && ctx.Err() != nil {
			err = context.Cause(ctx)
		}
	}()
	socket, err := managedEndpointSocketPath(instance)
	if err != nil {
		return ManagedEndpointAction{}, err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return ManagedEndpointAction{}, err
	}
	defer connection.Close()
	// DialContext alone cannot interrupt a later blocked read on manual
	// cancellation. Closing this probe/response socket grants no kill authority
	// and never authorizes resubmitting an accepted action.
	stopClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClose()
	deadline := time.Now().Add(5 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = connection.SetDeadline(deadline)
	request := managedEndpointWorkerRequest{Version: ManagedEndpointOwnerProtocol, InstanceID: instance.ID, Method: method, Action: action, Environment: environment}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return ManagedEndpointAction{}, err
	}
	decoder := json.NewDecoder(io.LimitReader(connection, 256<<10+1))
	decoder.DisallowUnknownFields()
	var response managedEndpointWorkerResponse
	if err := decoder.Decode(&response); err != nil {
		return ManagedEndpointAction{}, err
	}
	if response.Version != ManagedEndpointOwnerProtocol || response.InstanceID != instance.ID || response.WorkerIdentity != worker.Identity {
		return ManagedEndpointAction{}, ErrManagedEndpointOwnershipUnknown
	}
	if response.Failure != "" {
		switch response.FailureCode {
		case "stale_action":
			return response.Action, ErrManagedEndpointStaleAction
		case "outcome_unknown":
			return response.Action, ErrManagedEndpointOutcomeUnknown
		case "invalid_authority":
			return response.Action, ErrManagedEndpointOwnershipUnknown
		case "operation_failed":
			return response.Action, errors.New(response.Failure)
		default:
			return response.Action, errors.New("managed endpoint worker failure protocol is invalid")
		}
	}
	if response.FailureCode != "" {
		return response.Action, errors.New("managed endpoint worker supplied a failure code without a failure")
	}
	if method == "hello" {
		if response.Action != (ManagedEndpointAction{}) {
			return response.Action, ErrManagedEndpointOwnershipUnknown
		}
	} else if response.Action.Ref != action.Ref {
		return response.Action, ErrManagedEndpointStaleAction
	}
	return response.Action, nil
}

func (runtime *ManagedEndpointSystemRuntime) wait(ctx context.Context, instance ManagedEndpointOwnerInstance, worker managedEndpointUnitObservation, action ManagedEndpointAction) (ManagedEndpointOwnerReceipt, error) {
	for {
		observed, err := runtime.call(ctx, instance, worker, "observe", action, nil)
		if err != nil {
			return ManagedEndpointOwnerReceipt{}, err
		}
		if observed.Receipt != nil && !observed.CleanupPending {
			return *observed.Receipt, nil
		}
		select {
		case <-ctx.Done():
			return ManagedEndpointOwnerReceipt{}, context.Cause(ctx)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (runtime *ManagedEndpointSystemRuntime) Start(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction, environment map[string]string) (ManagedEndpointOwnerReceipt, error) {
	if err := runtime.validate(instance, action); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if action.Ref.Kind != "start" {
		return ManagedEndpointOwnerReceipt{}, ErrManagedEndpointStaleAction
	}
	if action.Accepted {
		return runtime.Observe(ctx, instance, action)
	}
	worker, err := runtime.worker(ctx, instance, true, false)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if _, err := runtime.call(ctx, instance, worker, "submit", action, environment); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	receipt, err := runtime.wait(ctx, instance, worker, action)
	if err != nil {
		return receipt, err
	}
	return runtime.finalize(ctx, instance, worker, receipt)
}

func (runtime *ManagedEndpointSystemRuntime) finalize(ctx context.Context, instance ManagedEndpointOwnerInstance, worker managedEndpointUnitObservation, receipt ManagedEndpointOwnerReceipt) (ManagedEndpointOwnerReceipt, error) {
	if (receipt.Failure != "" || receipt.Ref.Kind == "stop") && instance.Registration.Owner.Kind == "native-unit" && instance.Registration.Owner.Adopt == nil {
		if receipt.Ref.Kind == "start" {
			won, err := runtime.store.AcquireManagedEndpointCleanup(ctx, receipt.Ref, &receipt)
			if err != nil {
				return receipt, err
			}
			if !won {
				return receipt, ErrManagedEndpointStaleAction
			}
		}
		owned := instance
		owned.Identity = worker.Identity
		proof, cleanupErr := runtime.unit.stop(ctx, owned)
		if cleanupErr != nil {
			return receipt, cleanupErr
		}
		receipt.Identity = worker.Identity
		receipt.OwnerVerified = true
		receipt.Stopped = true
		receipt.StoppedProof = proof
	}
	if instance.Registration.Owner.Kind == "docker" && receipt.HasPositiveStoppedProof(instance) {
		owned := instance
		owned.Registration.Owner = &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
		owned.Identity = worker.Identity
		if _, err := runtime.unit.stop(ctx, owned); err != nil {
			return receipt, err
		}
	}
	return receipt, nil
}

func (runtime *ManagedEndpointSystemRuntime) Stop(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction, environment map[string]string) (ManagedEndpointOwnerReceipt, error) {
	if err := runtime.validate(instance, action); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if action.Ref.Kind != "stop" {
		return ManagedEndpointOwnerReceipt{}, ErrManagedEndpointStaleAction
	}
	if receipt, done, err := runtime.noOwnerCreatedReceipt(ctx, instance, action); err != nil || done {
		return receipt, err
	}
	if action.Accepted {
		return runtime.Observe(ctx, instance, action)
	}
	if receipt, done, err := runtime.stoppedNativeReceipt(ctx, instance, action); err != nil || done {
		return receipt, err
	}
	if receipt, done, err := runtime.stoppedDockerReceipt(ctx, instance, action); err != nil || done {
		return receipt, err
	}
	worker, err := runtime.worker(ctx, instance, true, instance.Registration.Owner.Kind == "docker")
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if _, err := runtime.call(ctx, instance, worker, "submit", action, environment); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	receipt, err := runtime.wait(ctx, instance, worker, action)
	if err != nil {
		return receipt, err
	}
	return runtime.finalize(ctx, instance, worker, receipt)
}

func (runtime *ManagedEndpointSystemRuntime) Observe(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction) (ManagedEndpointOwnerReceipt, error) {
	if err := runtime.validate(instance, action); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if action.Ref.Kind == "stop" && !action.Accepted {
		if receipt, done, err := runtime.noOwnerCreatedReceipt(ctx, instance, action); err != nil || done {
			return receipt, err
		}
	}
	if receipt, done, err := runtime.stoppedNativeReceipt(ctx, instance, action); err != nil || done {
		return receipt, err
	}
	if receipt, done, err := runtime.stoppedDockerReceipt(ctx, instance, action); err != nil || done {
		return receipt, err
	}
	worker, err := runtime.worker(ctx, instance, false, false)
	if err != nil {
		return runtime.recoverDockerReceipt(ctx, instance, action, err)
	}
	observed, err := runtime.call(ctx, instance, worker, "observe", action, nil)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if observed.Receipt == nil || observed.CleanupPending {
		return ManagedEndpointOwnerReceipt{}, ErrManagedEndpointActionPending
	}
	receipt := *observed.Receipt
	return runtime.finalize(ctx, instance, worker, receipt)
}

func (runtime *ManagedEndpointSystemRuntime) Cancel(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction, environment map[string]string) (ManagedEndpointOwnerReceipt, error) {
	if err := runtime.validate(instance, action); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	current, canonical, err := runtime.store.ReadManagedEndpointAction(ctx, action.Ref)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if !canonical.CancelRequested {
		return ManagedEndpointOwnerReceipt{}, ErrManagedEndpointStaleAction
	}
	instance = current
	action.CancelRequested = true
	if action.Ref.Kind == "stop" && !canonical.Accepted {
		if receipt, done, err := runtime.noOwnerCreatedReceipt(ctx, instance, action); err != nil || done {
			if done {
				receipt.Failure = "managed endpoint stop cancelled before admission"
			}
			return receipt, err
		}
	}
	initialAdoption := managedEndpointInitialAdoption(instance, action.Ref)
	if !initialAdoption {
		won, err := runtime.store.AcquireManagedEndpointCleanup(ctx, action.Ref, nil)
		if err != nil {
			return ManagedEndpointOwnerReceipt{}, err
		}
		if !won {
			return ManagedEndpointOwnerReceipt{}, ErrManagedEndpointStaleAction
		}
	}
	worker, err := runtime.unit.observe(ctx, instance)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if !canonical.Accepted {
		if action.Ref.Kind == "stop" {
			return ManagedEndpointOwnerReceipt{Ref: action.Ref, Failure: "managed endpoint stop cancelled before execution admission"}, nil
		}
		if !worker.Absent && !worker.Empty && worker.Identity.Kind == "native-unit" {
			owned := instance
			owned.Registration.Owner = &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
			owned.Identity = worker.Identity
			if _, err := runtime.unit.stop(ctx, owned); err != nil {
				return ManagedEndpointOwnerReceipt{}, err
			}
		}
		return ManagedEndpointOwnerReceipt{Ref: action.Ref, NotStarted: true, Failure: "managed endpoint action cancelled before execution admission"}, nil
	}
	worker, err = runtime.worker(ctx, instance, false, false)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	if _, err := runtime.call(ctx, instance, worker, "cancel", action, environment); err != nil {
		return ManagedEndpointOwnerReceipt{}, err
	}
	receipt, err := runtime.wait(ctx, instance, worker, action)
	if err != nil {
		return receipt, err
	}
	if receipt.Failure == "" {
		receipt.Failure = "managed endpoint action cancelled before handoff"
	}
	if instance.Registration.Owner.Kind == "native-unit" && !initialAdoption {
		owned := instance
		owned.Identity = worker.Identity
		proof, err := runtime.unit.stop(ctx, owned)
		if err != nil {
			return receipt, err
		}
		receipt.Identity = worker.Identity
		receipt.OwnerVerified = true
		receipt.Stopped = true
		receipt.StoppedProof = proof
	}
	if !initialAdoption && instance.Registration.Owner.Kind == "docker" {
		docker, err := newManagedEndpointDocker()
		if err != nil {
			return receipt, err
		}
		owned := instance
		if receipt.Identity.Kind == "docker" {
			owned.Identity = receipt.Identity
		}
		proof, err := docker.observeStopped(ctx, owned)
		if err != nil {
			return receipt, errors.Join(ErrManagedEndpointOutcomeUnknown, err)
		}
		receipt.Identity = proof.Identity
		receipt.OwnerVerified = true
		receipt.Stopped = true
		receipt.StoppedProof = proof
		return runtime.finalize(ctx, owned, worker, receipt)
	}
	return receipt, nil
}

func (runtime *ManagedEndpointSystemRuntime) Check(ctx context.Context, instance ManagedEndpointOwnerInstance) error {
	if err := instance.Validate(); err != nil {
		return err
	}
	if instance.Identity == (ManagedEndpointRuntimeIdentity{}) {
		return ErrManagedEndpointOwnershipUnknown
	}
	var worker managedEndpointUnitObservation
	if instance.Registration.Owner.Kind != "docker" {
		var err error
		worker, err = runtime.worker(ctx, instance, false, false)
		if err != nil {
			return err
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	receipt := ManagedEndpointOwnerReceipt{}
	(&managedEndpointWorker{identity: worker.Identity}).verifyReady(probeCtx, instance, &receipt)
	if receipt.Failure != "" {
		return errors.New(receipt.Failure)
	}
	if receipt.Identity != instance.Identity || !receipt.Readiness {
		return ErrManagedEndpointOwnershipUnknown
	}
	return nil
}
