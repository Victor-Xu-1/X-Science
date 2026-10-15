//go:build linux

package detached

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// A deadline can expire before its context timer is scheduled to publish the
// cancellation. Hold that publication explicitly instead of relying on two
// real timers to race. The backing context still owns Done and the exact cause.
type heldDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (ctx heldDeadlineContext) Deadline() (time.Time, bool) { return ctx.deadline, true }

func (ctx heldDeadlineContext) Err() error {
	if ctx.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func TestCommandClientWaitsForCallerDeadlinePublication(t *testing.T) {
	received := make(chan struct{})
	client, peerDone := commandCancellationFixture(t, func(connection *net.UnixConn, _ CommandRequest) error {
		close(received)
		_, err := io.Copy(io.Discard, connection)
		return err
	})
	cause := errors.New("owned caller deadline was published")
	parent, publishCause := context.WithCancelCause(context.Background())
	defer publishCause(nil)
	ctx := heldDeadlineContext{Context: parent, deadline: time.Now().Add(250 * time.Millisecond)}
	finished := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, cancellationCommand())
		finished <- err
	}()
	select {
	case <-received:
	case err := <-finished:
		t.Fatalf("client finished before the real peer received its request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("real Unix peer did not receive the command")
	}

	// Keep the caller's Done open throughout an already-expired socket budget.
	// This bounded observation is not another cancellation timer: only the
	// explicit publishCause call below is allowed to decide the caller outcome.
	observation := time.NewTimer(time.Until(ctx.deadline) + time.Second)
	defer observation.Stop()
	var callErr error
	completedBeforeCause := false
	select {
	case callErr = <-finished:
		completedBeforeCause = true
	case <-observation.C:
	}
	if ctx.Err() != nil || context.Cause(ctx) != nil {
		t.Fatal("fixture published the caller cause before the explicit release")
	}
	publishCause(cause)
	if !completedBeforeCause {
		select {
		case callErr = <-finished:
		case <-time.After(time.Second):
			t.Fatal("caller cancellation did not release the blocked RPC")
		}
	}
	if completedBeforeCause {
		t.Errorf("socket deadline decided the RPC before caller cause publication: %v", callErr)
	}
	if !errors.Is(callErr, cause) {
		t.Errorf("RPC did not preserve the exact published caller cause: %v", callErr)
	}
	select {
	case err := <-peerDone:
		if err != nil {
			t.Fatalf("client cancellation did not close its owned peer cleanly: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client connection or peer handler leaked after cancellation")
	}
}
