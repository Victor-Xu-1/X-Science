package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"x-science/internal/compute"
)

// This is the real SQLite/publication ordering contract, not a physical unit or
// script witness. A second Store owns a distinct connection to the same DB.
func TestManagedEndpointPublicationFenceOutlivesCallerCancellation(t *testing.T) {
	for _, cancelPublisher := range []bool{false, true} {
		name := "ordinary"
		if cancelPublisher {
			name = "cancelled_callback"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newManagedEndpointOwnerFixture(t)
			other, err := OpenExisting(fixture.instance.WorkspacePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			input, err := compute.ManagedEndpointBootstrapFor(fixture.instance, fixture.action.Ref)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			releaseWriter := func() { once.Do(func() { close(release) }) }
			t.Cleanup(releaseWriter)
			marker := filepath.Join(t.TempDir(), "publication")
			go func() {
				finished <- fixture.store.WithManagedEndpointBootstrapPublication(ctx, input, managedOwnerRecoveryIdentity(fixture), func(instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction) error {
					if !input.Matches(instance) || action.Ref != input.RequestedAction || action.Accepted {
						return errors.New("publisher changed canonical scope or execution admission")
					}
					close(entered)
					<-release
					return os.WriteFile(marker, []byte("synchronous private publication"), 0o600)
				})
			}()
			select {
			case <-entered:
			case err := <-finished:
				t.Fatalf("publisher did not enter: %v", err)
			case <-time.After(time.Second):
				t.Fatal("publisher did not enter its bounded callback")
			}
			if cancelPublisher {
				cancel()
			}
			attemptCtx, stopAttempt := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer stopAttempt()
			type cancellation struct {
				won bool
				err error
			}
			cancelled := make(chan cancellation, 1)
			go func() {
				won, err := other.CancelManagedEndpointAction(attemptCtx, fixture.action.Ref)
				cancelled <- cancellation{won, err}
			}()
			select {
			case result := <-cancelled:
				if result.won || result.err == nil {
					t.Fatalf("cancellation committed while FS callback was active: won=%t err=%v", result.won, result.err)
				}
			case <-time.After(time.Second):
				t.Fatal("contending cancellation ignored its finite context")
			}
			releaseWriter()
			select {
			case err := <-finished:
				if cancelPublisher && !errors.Is(err, context.Canceled) || !cancelPublisher && err != nil {
					t.Fatalf("publisher completion/cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("publisher did not release its fence after callback returned")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("callback did not finish synchronously: %v", err)
			}
			if won, err := other.CancelManagedEndpointAction(t.Context(), fixture.action.Ref); err != nil || !won {
				t.Fatalf("cancellation after callback: won=%t err=%v", won, err)
			}
			_, action, err := other.ReadManagedEndpointAction(t.Context(), fixture.action.Ref)
			if err != nil || !action.CancelRequested || action.Accepted || action.Receipt != nil {
				t.Fatalf("publication fabricated execution or lost cancellation: %+v err=%v", action, err)
			}
		})
	}
}

func TestManagedEndpointBootstrapPublicationUsesExactCurrentAuthority(t *testing.T) {
	for _, scenario := range []string{"current_start", "cancelled_start", "retired_start", "superseded_start", "scope_mismatch", "current_no_owner_stop", "cancelled_no_owner_stop", "accepted_live_owner"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newManagedEndpointOwnerFixture(t)
			instance, action := fixture.instance, fixture.action
			allowed := scenario == "current_start" || scenario == "accepted_live_owner" || scenario == "current_no_owner_stop" || scenario == "cancelled_no_owner_stop"
			identity := managedOwnerRecoveryIdentity(fixture)
			switch scenario {
			case "cancelled_start", "retired_start":
				if won, err := fixture.store.CancelManagedEndpointAction(t.Context(), action.Ref); err != nil || !won {
					t.Fatalf("cancel: won=%t err=%v", won, err)
				}
				if scenario == "retired_start" {
					// This fixture has never dispatched any runtime/script. Its
					// negative receipt is not a fabricated physical stopped proof.
					receipt := compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, NotStarted: true, Failure: "never dispatched test fixture"}
					if state, err := fixture.store.FinishManagedEndpointAction(t.Context(), action.Ref, receipt); err != nil || state != "failed" {
						t.Fatalf("negative finish: state=%q err=%v", state, err)
					}
				}
			case "superseded_start", "current_no_owner_stop", "cancelled_no_owner_stop":
				if _, run, err := fixture.store.ClaimManagedEndpointStop(instance.Endpoint, instance.UserID, instance.RuntimeRoot); err != nil || !run {
					t.Fatalf("Stop claim: run=%t err=%v", run, err)
				}
				if scenario != "superseded_start" {
					var err error
					instance, action, err = fixture.store.CurrentManagedEndpointAction(t.Context(), instance.ID)
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "cancelled_no_owner_stop" {
						if won, err := fixture.store.CancelManagedEndpointAction(t.Context(), action.Ref); err != nil || !won {
							t.Fatalf("Stop cancel: won=%t err=%v", won, err)
						}
					}
					if reserved, err := fixture.store.ReserveManagedEndpointNoOwnerCleanup(t.Context(), action.Ref); err != nil || !reserved {
						t.Fatalf("NoOwner reservation: reserved=%t err=%v", reserved, err)
					}
				}
			case "accepted_live_owner":
				identity = fixture.ready(t)
			}
			input, err := compute.ManagedEndpointBootstrapFor(instance, action.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "scope_mismatch" {
				input.Scope.ServiceDir = t.TempDir()
			}
			called := false
			err = fixture.store.WithManagedEndpointBootstrapPublication(t.Context(), input, identity, func(current compute.ManagedEndpointOwnerInstance, actual compute.ManagedEndpointAction) error {
				called = true
				if !input.Matches(current) || actual.Ref != input.RequestedAction {
					return errors.New("published under a different scope/ref")
				}
				if scenario == "current_no_owner_stop" || scenario == "cancelled_no_owner_stop" {
					if actual.Accepted || current.CleanupRef == nil || *current.CleanupRef != current.InitialAction {
						return errors.New("NoOwner publication lost its genuine reservation or admitted a script")
					}
				}
				return nil
			})
			if allowed && (err != nil || !called) || !allowed && (err == nil || called) {
				t.Fatalf("scenario=%s allowed=%t callback=%t err=%v", scenario, allowed, called, err)
			}
		})
	}
}
