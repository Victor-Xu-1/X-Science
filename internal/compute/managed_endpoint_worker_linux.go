//go:build linux

package compute

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

type managedEndpointWorkerRequest struct {
	Version     int                   `json:"version"`
	InstanceID  string                `json:"instanceId"`
	Method      string                `json:"method"`
	Action      ManagedEndpointAction `json:"action"`
	Environment map[string]string     `json:"environment,omitempty"`
}

type managedEndpointWorkerResponse struct {
	Version        int                            `json:"version"`
	InstanceID     string                         `json:"instanceId"`
	WorkerIdentity ManagedEndpointRuntimeIdentity `json:"workerIdentity"`
	Action         ManagedEndpointAction          `json:"action"`
	Failure        string                         `json:"failure,omitempty"`
	FailureCode    string                         `json:"failureCode,omitempty"`
}

type managedEndpointWorker struct {
	mu              sync.Mutex
	execution       sync.Mutex
	instance        ManagedEndpointOwnerInstance
	unit            *managedEndpointUnit
	identity        ManagedEndpointRuntimeIdentity
	output          *managedEndpointOutput
	writer          *os.File
	reader          *os.File
	readerDone      chan error
	action          ManagedEndpointAction
	cancel          context.CancelFunc
	journalPath     string
	recoveredOutput bool
	store           ManagedEndpointOwnerStore
	cleanupRunning  bool
}

// RunManagedEndpointOwnerWorker is a private CLI entry. It owns the output
// descriptors and operation receipts independently of the web process. It never
// executes a script merely because a previous action journal is incomplete.
func RunManagedEndpointOwnerWorker(ctx context.Context, descriptor string, store ManagedEndpointOwnerStore) error {
	var instance ManagedEndpointOwnerInstance
	if err := readManagedEndpointPrivateJSON(descriptor, &instance); err != nil {
		return err
	}
	if err := instance.Validate(); err != nil {
		return err
	}
	if store == nil {
		return errors.New("managed endpoint worker has no durable admission authority")
	}
	current, initial, err := store.ReadManagedEndpointAction(ctx, instance.InitialAction)
	if err != nil {
		return err
	}
	if current.ID != instance.ID || current.UserID != instance.UserID || current.ApprovedHash != instance.ApprovedHash || current.RuntimeRoot != instance.RuntimeRoot || current.ServiceDir != instance.ServiceDir || current.WorkspacePath != instance.WorkspacePath || current.InitialAction != instance.InitialAction {
		return ErrManagedEndpointCorruptOwner
	}
	if initial.CancelRequested && !initial.Accepted || initial.Receipt != nil && initial.Receipt.NotStarted {
		stopAuthority, successor, err := store.CurrentManagedEndpointAction(ctx, instance.ID)
		if errors.Is(err, ErrManagedEndpointStaleAction) {
			return nil
		}
		if err != nil {
			return err
		}
		if successor.Ref.Kind != "stop" || successor.Ref == instance.InitialAction || successor.Receipt != nil {
			return nil
		}
		if successor.CancelRequested && (stopAuthority.CleanupRef == nil || *stopAuthority.CleanupRef != instance.InitialAction) {
			return nil
		}
	}
	instance = current
	if descriptor != filepath.Join(managedEndpointInstanceDirectory(instance), "owner.json") {
		return errors.New("managed endpoint descriptor path does not match its instance")
	}
	unit, err := newManagedEndpointUnit()
	if err != nil {
		return err
	}
	observed, err := unit.observe(ctx, instance)
	if err != nil || observed.Identity.PID != int64(os.Getpid()) {
		return errors.New("managed endpoint worker is not the positively identified unit owner")
	}
	outputDir := filepath.Join(managedEndpointInstanceDirectory(instance), "output")
	output, recovered, err := managedEndpointOutputForWorker(outputDir, instance.Registration.Owner.Output)
	if err != nil {
		return err
	}
	defer output.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	worker := &managedEndpointWorker{instance: instance, unit: unit, identity: observed.Identity, output: output, reader: reader, writer: writer, readerDone: make(chan error, 1), journalPath: filepath.Join(managedEndpointInstanceDirectory(instance), "action.json"), recoveredOutput: recovered, store: store}
	if err := readManagedEndpointPrivateJSON(worker.journalPath, &worker.action); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = reader.Close()
		_ = writer.Close()
		return err
	}
	if worker.action.Ref.InstanceID != "" && worker.action.Ref.InstanceID != instance.ID {
		_ = reader.Close()
		_ = writer.Close()
		return ErrManagedEndpointCorruptOwner
	}
	if worker.action.Ref.InstanceID != "" {
		if err := worker.action.Ref.Validate(); err != nil {
			_ = reader.Close()
			_ = writer.Close()
			return err
		}
		_, canonical, err := store.ReadManagedEndpointAction(ctx, worker.action.Ref)
		if err != nil {
			_ = reader.Close()
			_ = writer.Close()
			return err
		}
		worker.action.Accepted = canonical.Accepted
		worker.action.CancelRequested = canonical.CancelRequested
	}
	if worker.action.Receipt != nil && worker.action.Receipt.OwnerVerified && worker.action.Receipt.Identity != (ManagedEndpointRuntimeIdentity{}) {
		if err := worker.action.Receipt.Identity.ValidateFor(instance); err != nil {
			_ = reader.Close()
			_ = writer.Close()
			return err
		}
		worker.instance.Identity = worker.action.Receipt.Identity
	}
	go func() { _, copyErr := io.Copy(output, reader); worker.readerDone <- copyErr }()
	defer func() { _ = writer.Close(); _ = reader.Close(); <-worker.readerDone }()
	socket, err := managedEndpointSocketPath(instance)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	stopClose := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopClose()
	connections := make(chan struct{}, 16)
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		}
		select {
		case connections <- struct{}{}:
		default:
			_ = connection.Close()
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { <-connections }()
			defer connection.Close()
			worker.serveConnection(connection)
		}()
	}
}
