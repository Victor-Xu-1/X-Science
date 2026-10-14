//go:build linux

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestKernelSessionGateWaitRetainsPreparationBudgetAndCancellation(t *testing.T) {
	for _, mode := range []string{"preparation_budget", "caller_cancel"} {
		t.Run(mode, func(t *testing.T) {
			x := newResourceContinuationFixture(t)
			task, stopTask := context.WithCancel(context.Background())
			defer stopTask()
			first, stopFirst := context.WithCancel(context.WithValue(task, sessionRunLifetimeContextKey{}, task))
			defer stopFirst()
			firstDone := x.start(first)
			firstJoined := false
			defer func() {
				stopFirst()
				if !firstJoined {
					select {
					case <-firstDone:
					case <-time.After(3 * time.Second):
						t.Error("resource fixture did not release its session gate")
					}
				}
			}()
			select {
			case <-x.launcher.observed:
			case err := <-firstDone:
				firstJoined = true
				t.Fatalf("first invocation did not own the capacity wait: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("first invocation did not reach capacity admission")
			}
			var backendID string
			if err := x.f.db.QueryRow(`SELECT backend_id FROM kernel_execution_backends WHERE frame_id=?`, x.f.stream.FrameID).Scan(&backendID); err != nil {
				t.Fatal(err)
			}
			backend, found, err := x.f.store.GetKernelExecutionBackend(task, backendID)
			if err != nil || !found {
				t.Fatalf("read real backend found=%t err=%v", found, err)
			}
			durable, err := workspace.DecodeKernelExecutionSessionSpecV1(backend.SessionSpecJSON)
			if err != nil {
				t.Fatal(err)
			}
			request, cancelRequest := context.WithCancelCause(context.WithValue(task, sessionRunLifetimeContextKey{}, task))
			defer cancelRequest(nil)
			limit := 100 * time.Millisecond
			want := error(errSessionRunnerKernelRecoveryDeadline)
			if mode == "caller_cancel" {
				limit = time.Second
				want = errors.New("session gate caller lease was revoked")
			}
			budget, closeBudget := withKernelRecoveryPreparationBudget(request, limit)
			defer closeBudget()
			setup, closeSetup := detachedKernelSetupContext(budget, 0)
			defer closeSetup()
			secondDone := make(chan error, 1)
			go func() {
				_, err := x.backend.EnsureSession(setup, recoveredKernelSessionSpec(durable))
				secondDone <- err
			}()
			if mode == "caller_cancel" {
				select {
				case err := <-secondDone:
					t.Fatalf("second invocation did not remain behind the owned gate: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				cancelRequest(want)
			}
			select {
			case err := <-secondDone:
				if !errors.Is(err, want) || !errors.Is(context.Cause(budget), want) {
					t.Fatalf("session gate lost preparation/caller cause: err=%v cause=%v want=%v", err, context.Cause(budget), want)
				}
			case <-time.After(2 * time.Second):
				cancelRequest(context.Canceled)
				stopFirst()
				select {
				case <-secondDone:
				case <-time.After(time.Second):
					t.Error("second waiter did not join after cleanup")
				}
				t.Fatal("session gate paused preparation or ignored caller cancellation")
			}
			select {
			case err := <-firstDone:
				firstJoined = true
				t.Fatalf("second waiter disturbed the first resource wait: %v", err)
			default:
			}
			reservation, found, err := x.f.store.KernelResourceReservation(task, backendID, backend.BackendGeneration)
			if err != nil || !found || reservation.State != "waiting" || reservation.ReservedBytes != 0 || x.launcher.launched.Load() || task.Err() != nil {
				t.Fatalf("gate wait changed durable execution: reservation=%+v found=%t err=%v", reservation, found, err)
			}
		})
	}
}
