//go:build linux

package compute

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type managedEndpointWorkerGateStore struct {
	original ManagedEndpointAction
	failure  error
	accepts  int
}

func (store *managedEndpointWorkerGateStore) AcquireManagedEndpointCleanup(context.Context, ManagedEndpointActionRef, *ManagedEndpointOwnerReceipt) (bool, error) {
	panic("unexpected cleanup admission")
}
func (store *managedEndpointWorkerGateStore) SettleSupersededManagedEndpointAction(context.Context, ManagedEndpointActionRef, *ManagedEndpointOwnerReceipt) (bool, error) {
	panic("unexpected receipt settlement")
}
func (store *managedEndpointWorkerGateStore) CurrentManagedEndpointAction(context.Context, string) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error) {
	panic("unexpected current action")
}
func (store *managedEndpointWorkerGateStore) ReserveManagedEndpointNoOwnerCleanup(context.Context, ManagedEndpointActionRef) (bool, error) {
	panic("unexpected no-owner cleanup")
}

func (store *managedEndpointWorkerGateStore) PrepareManagedEndpointAction(context.Context, ManagedEndpointClaim, string, string) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error) {
	panic("unexpected prepare")
}
func (store *managedEndpointWorkerGateStore) AcceptManagedEndpointAction(context.Context, ManagedEndpointActionRef, ManagedEndpointRuntimeIdentity) (bool, error) {
	store.accepts++
	return false, store.failure
}
func (store *managedEndpointWorkerGateStore) ReadManagedEndpointAction(context.Context, ManagedEndpointActionRef) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error) {
	return ManagedEndpointOwnerInstance{}, store.original, nil
}
func (store *managedEndpointWorkerGateStore) RecordManagedEndpointObservationFailure(context.Context, ManagedEndpointActionRef, error) error {
	panic("unexpected observation mutation")
}
func (store *managedEndpointWorkerGateStore) CancelManagedEndpointAction(context.Context, ManagedEndpointActionRef) (bool, error) {
	panic("unexpected cancel")
}
func (store *managedEndpointWorkerGateStore) FinishManagedEndpointAction(context.Context, ManagedEndpointActionRef, ManagedEndpointOwnerReceipt) (string, error) {
	panic("unexpected finish")
}
func (store *managedEndpointWorkerGateStore) PendingManagedEndpointActions(context.Context, *ManagedEndpointActionRef, int) ([]ManagedEndpointActionRef, error) {
	panic("unexpected scan")
}
func (store *managedEndpointWorkerGateStore) ReadManagedEndpointLiveOwner(context.Context, string, string) (ManagedEndpointOwnerInstance, error) {
	panic("unexpected live owner")
}

func TestManagedEndpointWorkerRejectsHigherRefWithoutMutatingCurrentOwner(t *testing.T) {
	for _, failure := range []error{ErrManagedEndpointStaleAction, io.ErrUnexpectedEOF} {
		t.Run(failure.Error(), func(t *testing.T) {
			dir := t.TempDir()
			journal := filepath.Join(dir, "action.json")
			original := ManagedEndpointAction{Ref: ManagedEndpointActionRef{InstanceID: "private-gate", Kind: "start", Generation: 1, ClaimHandle: "original"}, Accepted: true, CancelRequested: true}
			if err := writeManagedEndpointPrivateJSON(journal, original); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &managedEndpointWorkerGateStore{original: original, failure: failure}
			worker := &managedEndpointWorker{instance: ManagedEndpointOwnerInstance{ID: "private-gate", UserID: "owner-a", ServiceDir: dir, Registration: ManagedEndpointRegistration{Port: 25001}}, action: original, cancel: cancel, journalPath: journal, store: store}
			foreign := original.Ref
			foreign.Generation++
			foreign.ClaimHandle = "foreign"
			err = worker.submit(managedEndpointWorkerRequest{Action: ManagedEndpointAction{Ref: foreign}, Environment: map[string]string{"HOST_PORT": "25001", "SERVICE_DIR": dir}})
			if !errors.Is(err, failure) || store.accepts != 1 {
				t.Fatalf("gate err=%v calls=%d", err, store.accepts)
			}
			if ctx.Err() != nil || !reflect.DeepEqual(worker.action, original) {
				t.Fatalf("rejected ref changed/cancelled original action: %#v cause=%v", worker.action, context.Cause(ctx))
			}
			after, err := os.ReadFile(journal)
			if err != nil || string(after) != string(before) {
				t.Fatalf("rejected ref overwrote original journal: err=%v", err)
			}
		})
	}
}
