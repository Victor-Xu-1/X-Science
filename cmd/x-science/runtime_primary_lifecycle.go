package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// The runServer owner registers all primary workers synchronously before it
// begins shutdown. Nested worker pools keep their existing internal joins;
// detached executors and remote scientific jobs do not belong to this group.
type runtimePrimaryWorkers struct {
	group sync.WaitGroup
	once  sync.Once
	done  chan struct{}
}

func (workers *runtimePrimaryWorkers) Go(run func()) {
	workers.group.Go(run)
}

func (workers *runtimePrimaryWorkers) Wait(ctx context.Context) error {
	workers.once.Do(func() {
		workers.done = make(chan struct{})
		go func() {
			workers.group.Wait()
			close(workers.done)
		}()
	})
	select {
	case <-workers.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func shutdownPrimaryRuntime(ctx context.Context, cancel context.CancelFunc, workers *runtimePrimaryWorkers, closeTimeout time.Duration, closeRuntime func(context.Context) error) error {
	cancel()
	joinErr := workers.Wait(ctx)
	if joinErr != nil {
		joinErr = fmt.Errorf("join primary runtime workers: %w", joinErr)
	}
	// Cancellation/cleanup must still be attempted after an incomplete join.
	// An error is not evidence that the remaining writers have stopped.
	// Create the caller-owned cleanup budget only AFTER the join, retaining
	// diagnostic values without inheriting its exhausted deadline/cancellation.
	closeContext, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer closeCancel()
	closeErr := closeRuntime(closeContext)
	if closeErr != nil {
		closeErr = fmt.Errorf("close primary runtime: %w", closeErr)
	}
	return errors.Join(joinErr, closeErr)
}

type runtimeShutdownIncompleteError struct {
	cause error
}

func (err *runtimeShutdownIncompleteError) Error() string {
	return "runtime shutdown incomplete: " + err.cause.Error()
}

func (err *runtimeShutdownIncompleteError) Unwrap() error { return err.cause }

func withRuntimeShutdownFailure(runErr, shutdownErr error) error {
	if shutdownErr == nil {
		return runErr
	}
	return &runtimeShutdownIncompleteError{cause: errors.Join(runErr, shutdownErr)}
}

type runtimeRestartRequestedError struct {
	Reason string
}

func (err *runtimeRestartRequestedError) Error() string {
	return "runtime restart requested: " + err.Reason
}

func finishRuntimeRun(runErr error, launchReplacement func() error) error {
	// A restart marker hidden inside joined cleanup failures is not a clean
	// handoff. In particular, errors.As alone would still find that marker.
	var incomplete *runtimeShutdownIncompleteError
	if errors.As(runErr, &incomplete) {
		return runErr
	}
	restart, cleanRestart := runErr.(*runtimeRestartRequestedError)
	if !cleanRestart {
		return runErr
	}
	if err := launchReplacement(); err != nil {
		return fmt.Errorf("launch replacement after %s: %w", restart.Reason, err)
	}
	return nil
}
