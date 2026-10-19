package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKernelIdleReaperCloseTreatsParentCancellationAsNormalStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Worker.Close returns its closing context's Err after joining a worker
	// killed during cancellation, even though the parent requested this stop.
	if got := kernelIdleReaperCloseError(ctx, ctx.Err()); got != nil {
		t.Fatalf("parent cancellation returned as reaper failure: %v", got)
	}
}

func TestKernelIdleReaperCloseTreatsParentCancellationWithCauseAsNormalStop(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("supervisor stopped")
	cancel(cause)
	if context.Cause(ctx) != cause || ctx.Err() != context.Canceled {
		t.Fatal("custom cancellation fixture did not retain its cause")
	}
	// The worker returns ctx.Err(), not the arbitrary custom cancellation cause.
	if got := kernelIdleReaperCloseError(ctx, ctx.Err()); got != nil {
		t.Fatalf("parent cancellation with cause returned as failure: %v", got)
	}
}

func TestKernelIdleReaperClosePreservesIndependentFailures(t *testing.T) {
	cleanupErr := errors.New("kernel process did not exit after kill")
	tests := []struct {
		name       string
		cancelled  bool
		closeError func(*testing.T, context.Context) error
	}{
		{
			name: "live parent child deadline",
			closeError: func(t *testing.T, parent context.Context) error {
				child, cancel := context.WithDeadline(parent, time.Unix(1, 0))
				defer cancel()
				if child.Err() != context.DeadlineExceeded || parent.Err() != nil {
					t.Fatal("child deadline fixture cancelled its live parent")
				}
				return child.Err()
			},
		},
		{
			name:       "live parent independent cancellation",
			closeError: func(*testing.T, context.Context) error { return context.Canceled },
		},
		{
			name:       "live parent cleanup failure",
			closeError: func(*testing.T, context.Context) error { return cleanupErr },
		},
		{
			name:       "cancelled parent cleanup failure",
			cancelled:  true,
			closeError: func(*testing.T, context.Context) error { return cleanupErr },
		},
		{
			name:       "cancelled parent child deadline",
			cancelled:  true,
			closeError: func(*testing.T, context.Context) error { return context.DeadlineExceeded },
		},
		{
			name:       "cancelled parent mixed cleanup failure",
			cancelled:  true,
			closeError: func(*testing.T, context.Context) error { return errors.Join(context.Canceled, cleanupErr) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancelled {
				cancel()
			}
			closeErr := test.closeError(t, ctx)
			if got := kernelIdleReaperCloseError(ctx, closeErr); got != closeErr {
				t.Fatalf("independent close failure changed: got %v, want %v", got, closeErr)
			}
		})
	}
}
