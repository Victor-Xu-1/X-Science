package compute

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManagedEndpointActionContextUsesApprovedStartupBudget(t *testing.T) {
	ownerFor := func(t *testing.T, seconds string) *ManagedEndpointOwnerSpec {
		t.Helper()
		raw := `{"version":1,"kind":"native-unit","output":{"segmentBytes":4096,"segments":2}}`
		if seconds != "" {
			raw = strings.TrimSuffix(raw, "}") + `,"startupTimeoutSeconds":` + seconds + `}`
		}
		var owner ManagedEndpointOwnerSpec
		if err := json.Unmarshal([]byte(raw), &owner); err != nil {
			t.Fatal(err)
		}
		if err := owner.Validate(); err != nil {
			t.Fatalf("owner is invalid: %v", err)
		}
		return &owner
	}
	assertBudget := func(t *testing.T, owner *ManagedEndpointOwnerSpec, kind string, want time.Duration) {
		t.Helper()
		before := time.Now()
		ctx, cancel := managedEndpointActionContext(context.Background(), kind, owner)
		defer cancel()
		after := time.Now()
		deadline, present := ctx.Deadline()
		if !present || deadline.Before(before.Add(want)) || deadline.After(after.Add(want)) {
			t.Fatalf("action %s deadline=%v present=%t; want between %v and %v", kind, deadline, present, before.Add(want), after.Add(want))
		}
	}

	t.Run("default_start_budget", func(t *testing.T) {
		assertBudget(t, ownerFor(t, ""), "start", 2*time.Minute)
	})
	t.Run("explicit_start_budget", func(t *testing.T) {
		assertBudget(t, ownerFor(t, "300"), "start", 5*time.Minute)
	})
	t.Run("stop_keeps_existing_budget", func(t *testing.T) {
		assertBudget(t, ownerFor(t, "300"), "stop", time.Minute)
	})
	t.Run("earlier_worker_parent_deadline", func(t *testing.T) {
		want := time.Now().Add(30 * time.Second)
		parent, cancelParent := context.WithDeadline(context.Background(), want)
		defer cancelParent()
		ctx, cancel := managedEndpointActionContext(parent, "start", ownerFor(t, "300"))
		defer cancel()
		if got, present := ctx.Deadline(); !present || !got.Equal(want) {
			t.Fatalf("worker parent deadline changed: got=%v present=%t want=%v", got, present, want)
		}
	})
	t.Run("worker_parent_cancellation", func(t *testing.T) {
		parent, cancelParent := context.WithCancelCause(context.Background())
		defer cancelParent(nil)
		ctx, cancel := managedEndpointActionContext(parent, "start", ownerFor(t, "300"))
		defer cancel()
		cause := errors.New("worker-owned cancellation")
		cancelParent(cause)
		select {
		case <-ctx.Done():
		default:
			t.Fatal("action context ignored worker-parent cancellation")
		}
		if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(context.Cause(ctx), cause) {
			t.Fatalf("action cancellation changed: err=%v cause=%v", ctx.Err(), context.Cause(ctx))
		}
	})
}
