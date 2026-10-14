package main

import (
	"context"
	"errors"
	"fmt"
)

// Registrations belong to runServer's single startup/shutdown goroutine. An
// owner's Close must reject new work and join its writers before returning nil.
type runtimePrimaryClosers struct {
	entries []runtimePrimaryCloser
}

type runtimePrimaryCloser struct {
	name  string
	close func(context.Context) error
}

func (closers *runtimePrimaryClosers) Add(name string, close func(context.Context) error) {
	closers.entries = append(closers.entries, runtimePrimaryCloser{name, close})
}

func (closers *runtimePrimaryClosers) Close(ctx context.Context) error {
	var errs []error
	for index := len(closers.entries) - 1; index >= 0; index-- {
		entry := closers.entries[index]
		// Even after one timeout, remaining owners must receive cancellation.
		if err := entry.close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", entry.name, err))
		}
	}
	return errors.Join(errs...)
}

// Persistent stores stay open if any writer failed to join. The existing typed
// shutdown failure also prevents finishRuntimeRun from launching a replacement;
// closing stores regardless would turn a bounded join failure into data loss.
type runtimePrimaryResources struct {
	entries []runtimePrimaryResource
}

type runtimePrimaryResource struct {
	name  string
	close func() error
}

func (resources *runtimePrimaryResources) Add(name string, close func() error) {
	resources.entries = append(resources.entries, runtimePrimaryResource{name, close})
}

func (resources *runtimePrimaryResources) CloseAfter(runErr error) error {
	var incomplete *runtimeShutdownIncompleteError
	if errors.As(runErr, &incomplete) {
		return runErr
	}
	var errs []error
	for index := len(resources.entries) - 1; index >= 0; index-- {
		entry := resources.entries[index]
		if err := entry.close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", entry.name, err))
		}
	}
	return withRuntimeShutdownFailure(runErr, errors.Join(errs...))
}
