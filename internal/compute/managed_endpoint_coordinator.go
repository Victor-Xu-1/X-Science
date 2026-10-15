package compute

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrManagedEndpointActionPending = errors.New("managed endpoint has a positively observed active action")

type ManagedEndpointEnvironmentResolver func(context.Context, ManagedEndpointOwnerInstance, string) (map[string]string, error)

// ManagedEndpointCoordinator is the one start/stop/recovery authority. The web
// process owns observation, while the approved physical owner owns execution.
type ManagedEndpointCoordinator struct {
	Store            ManagedEndpointOwnerStore
	Runtime          ManagedEndpointOwnerRuntime
	RuntimeRoot      string
	Environment      ManagedEndpointEnvironmentResolver
	CancelAuthorized func(context.Context) bool
}

func (coordinator *ManagedEndpointCoordinator) Run(ctx context.Context, claim ManagedEndpointClaim) (string, ManagedEndpointOwnerReceipt, error) {
	if coordinator == nil || coordinator.Store == nil || coordinator.Runtime == nil || coordinator.Environment == nil {
		return "", ManagedEndpointOwnerReceipt{}, errors.New("managed endpoint lifecycle is unavailable")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", ManagedEndpointOwnerReceipt{}, err
	}
	instance, action, err := coordinator.Store.PrepareManagedEndpointAction(ctx, claim, hex.EncodeToString(nonce[:]), coordinator.RuntimeRoot)
	if err != nil {
		return "", ManagedEndpointOwnerReceipt{}, err
	}
	if action.Receipt != nil {
		return coordinator.finish(ctx, action.Ref, *action.Receipt, nil)
	}
	environment, err := coordinator.Environment(ctx, instance, action.Ref.Kind)
	if err != nil {
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_, current, readErr := coordinator.Store.ReadManagedEndpointAction(settleCtx, action.Ref)
		if readErr != nil {
			return "", ManagedEndpointOwnerReceipt{}, errors.Join(err, readErr)
		}
		if current.Accepted {
			return "", ManagedEndpointOwnerReceipt{}, errors.Join(err, coordinator.Store.RecordManagedEndpointObservationFailure(settleCtx, action.Ref, err))
		}
		if action.Ref.Kind == "stop" {
			observed, observeErr := coordinator.Runtime.Observe(settleCtx, instance, current)
			if observeErr == nil && observed.NoScript {
				return coordinator.finish(settleCtx, action.Ref, observed, nil)
			}
			return "", observed, errors.Join(err, observeErr, coordinator.Store.RecordManagedEndpointObservationFailure(settleCtx, action.Ref, err))
		}
		return coordinator.finish(settleCtx, action.Ref, ManagedEndpointOwnerReceipt{Ref: action.Ref, NotStarted: true, Failure: err.Error()}, err)
	}
	var receipt ManagedEndpointOwnerReceipt
	if action.Ref.Kind == "start" {
		receipt, err = coordinator.Runtime.Start(ctx, instance, action, environment)
	} else {
		receipt, err = coordinator.Runtime.Stop(ctx, instance, action, environment)
	}
	if ctx.Err() != nil {
		// Observer lifetime is not task lifetime. A disconnect, Web drain or
		// ordinary timeout leaves the accepted original ActionRef owned by its
		// worker. Only explicit typed task/user authority permits cleanup.
		if coordinator.CancelAuthorized == nil || !coordinator.CancelAuthorized(ctx) {
			return "", receipt, context.Cause(ctx)
		}
		// Only this successful CAS authorizes cleanup. A committed live owner
		// cannot be revoked by a late HTTP/task cancellation.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 75*time.Second)
		defer cancel()
		won, cancelErr := coordinator.Store.CancelManagedEndpointAction(cleanupCtx, action.Ref)
		if cancelErr != nil {
			return "", receipt, errors.Join(context.Cause(ctx), cancelErr)
		}
		if won {
			action.CancelRequested = true
			receipt, err = coordinator.Runtime.Cancel(cleanupCtx, instance, action, environment)
			if err != nil {
				return "", receipt, errors.Join(context.Cause(ctx), err, coordinator.Store.RecordManagedEndpointObservationFailure(cleanupCtx, action.Ref, err))
			}
			if receipt.Ref.InstanceID == "" {
				receipt.Ref = action.Ref
			}
			if receipt.Failure == "" {
				receipt.Failure = context.Cause(ctx).Error()
			}
			return coordinator.finish(cleanupCtx, action.Ref, receipt, errors.Join(context.Cause(ctx), err))
		}
		_, stored, readErr := coordinator.Store.ReadManagedEndpointAction(cleanupCtx, action.Ref)
		if readErr == nil && stored.Receipt != nil {
			return coordinator.finish(cleanupCtx, action.Ref, *stored.Receipt, nil)
		}
		return "", receipt, errors.Join(context.Cause(ctx), readErr)
	}
	if err != nil {
		observationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		recordErr := coordinator.Store.RecordManagedEndpointObservationFailure(observationCtx, action.Ref, err)
		return "", receipt, errors.Join(ErrManagedEndpointOutcomeUnknown, err, recordErr)
	}
	return coordinator.finish(ctx, action.Ref, receipt, err)
}

// CheckLive observes the original physical owner; it never restarts a script.
func (coordinator *ManagedEndpointCoordinator) CheckLive(ctx context.Context, userID, name string) error {
	instance, err := coordinator.Store.ReadManagedEndpointLiveOwner(ctx, userID, name)
	if err != nil {
		return err
	}
	return coordinator.Runtime.Check(ctx, instance)
}

func (coordinator *ManagedEndpointCoordinator) finish(ctx context.Context, ref ManagedEndpointActionRef, receipt ManagedEndpointOwnerReceipt, runErr error) (string, ManagedEndpointOwnerReceipt, error) {
	if receipt.Ref.InstanceID == "" {
		receipt.Ref = ref
	}
	if runErr != nil && receipt.Failure == "" {
		receipt.Failure = runErr.Error()
	}
	state, err := coordinator.Store.FinishManagedEndpointAction(ctx, ref, receipt)
	if err != nil {
		return state, receipt, errors.Join(runErr, err)
	}
	if receipt.Failure != "" {
		return state, receipt, fmt.Errorf("managed endpoint %s: %s", ref.Kind, receipt.Failure)
	}
	if state == "failed" {
		return state, receipt, errors.New("managed endpoint result was not proven by its owner receipt")
	}
	return state, receipt, runErr
}

// Reconcile observes accepted actions or first-dispatches the same unaccepted
// durable intent through the original admission CAS. It never creates a new
// claim/ref or replays an accepted script whose outcome is missing.
func (coordinator *ManagedEndpointCoordinator) Reconcile(ctx context.Context, after *ManagedEndpointActionRef, limit int) (*ManagedEndpointActionRef, error) {
	refs, err := coordinator.Store.PendingManagedEndpointActions(ctx, after, limit)
	if err != nil {
		return after, err
	}
	var last *ManagedEndpointActionRef
	for _, ref := range refs {
		copyRef := ref
		last = &copyRef
		instance, action, err := coordinator.Store.ReadManagedEndpointAction(ctx, ref)
		if errors.Is(err, ErrManagedEndpointStaleAction) {
			continue
		}
		if err != nil {
			return last, err
		}
		observeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var receipt ManagedEndpointOwnerReceipt
		if action.CancelRequested {
			environment, environmentErr := coordinator.Environment(observeCtx, instance, action.Ref.Kind)
			if environmentErr != nil {
				cancel()
				return last, environmentErr
			}
			receipt, err = coordinator.Runtime.Cancel(observeCtx, instance, action, environment)
		} else if !action.Accepted {
			environment, environmentErr := coordinator.Environment(observeCtx, instance, action.Ref.Kind)
			if environmentErr != nil {
				cancel()
				return last, environmentErr
			}
			if action.Ref.Kind == "start" {
				receipt, err = coordinator.Runtime.Start(observeCtx, instance, action, environment)
			} else {
				receipt, err = coordinator.Runtime.Stop(observeCtx, instance, action, environment)
			}
		} else {
			receipt, err = coordinator.Runtime.Observe(observeCtx, instance, action)
		}
		cancel()
		if errors.Is(err, ErrManagedEndpointActionPending) {
			continue
		}
		if ctx.Err() != nil {
			return last, context.Cause(ctx)
		}
		if err != nil {
			if errors.Is(err, ErrManagedEndpointStaleAction) && action.CancelRequested {
				if _, settleErr := coordinator.Store.SettleSupersededManagedEndpointAction(ctx, ref, nil); settleErr != nil {
					return last, settleErr
				}
				continue
			}
			if recordErr := coordinator.Store.RecordManagedEndpointObservationFailure(ctx, ref, err); recordErr != nil && !errors.Is(recordErr, ErrManagedEndpointStaleAction) {
				return last, recordErr
			}
			continue
		}
		if _, _, finishErr := coordinator.finish(ctx, ref, receipt, err); finishErr != nil {
			if errors.Is(finishErr, ErrManagedEndpointStaleAction) && action.CancelRequested {
				if _, settleErr := coordinator.Store.SettleSupersededManagedEndpointAction(ctx, ref, &receipt); settleErr != nil {
					return last, settleErr
				}
				continue
			}
			if !errors.Is(finishErr, ErrManagedEndpointStaleAction) {
				return last, finishErr
			}
		}
	}
	if len(refs) < limit {
		return nil, nil
	}
	return last, nil
}
