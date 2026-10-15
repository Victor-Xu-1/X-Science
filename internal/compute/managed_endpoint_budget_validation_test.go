package compute

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagedEndpointActionContextRejectsUnvalidatedStartupBudget(t *testing.T) {
	for _, test := range []struct {
		name    string
		seconds int64
	}{
		{"negative", -1},
		{"duration_overflow", 1<<63 - 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := &ManagedEndpointOwnerSpec{
				Version: 1, Kind: "native-unit", Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2},
				StartupTimeoutSeconds: test.seconds,
			}
			if err := owner.Validate(); !errors.Is(err, errManagedEndpointStartupBudgetInvalid) {
				t.Fatalf("registration validation error=%v", err)
			}
			ctx, cancel := managedEndpointActionContext(context.Background(), "start", owner)
			defer cancel()
			select {
			case <-ctx.Done():
			default:
				t.Fatal("invalid startup budget received an executable default context")
			}
			if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(context.Cause(ctx), errManagedEndpointStartupBudgetInvalid) {
				t.Fatalf("invalid budget lost its failure cause: err=%v cause=%v", ctx.Err(), context.Cause(ctx))
			}
		})
	}
	t.Run("largest_representable_seconds_is_not_an_arbitrary_host_cap", func(t *testing.T) {
		seconds := int64(time.Duration(1<<63-1) / time.Second)
		budget, err := managedEndpointStartupBudget(seconds)
		if err != nil || budget/time.Second != time.Duration(seconds) || budget <= 0 {
			t.Fatalf("representable startup budget=%v err=%v", budget, err)
		}
	})
}
