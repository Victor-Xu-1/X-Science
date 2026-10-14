package server

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	kernelruntime "x-science/internal/kernel"
)

func TestKernelRecoveryPreparationBudgetChargesOnlyPreparing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, closeBudget := withKernelRecoveryPreparationBudget(context.Background(), 100*time.Millisecond)
		defer closeBudget()
		budget := kernelRecoveryPreparationBudgetFrom(ctx)
		setup, closeSetup := detachedKernelSetupContext(ctx, 0)
		defer closeSetup()
		admission := setup.(kernelruntime.ResourceAdmissionWaitContextSource)
		if admission.ResourceAdmissionWaitContext() != ctx || admission.ResourceAdmissionWaitContext() != ctx {
			t.Fatal("admission lookup changed the original cancellation authority")
		}
		time.Sleep(40 * time.Millisecond)
		resume := setup.(kernelruntime.ResourceCapacityWaitScopeSource).BeginResourceCapacityWait()
		time.Sleep(time.Hour)
		if ctx.Err() != nil {
			t.Fatal("resource queue consumed the preparation budget")
		}
		resume()
		resume() // Repeated cleanup must not refill or start an additional timer.
		time.Sleep(59 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("remaining preparation budget expired early")
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		if !errors.Is(context.Cause(ctx), errSessionRunnerKernelRecoveryDeadline) {
			t.Fatalf("active preparation did not exhaust its original remaining budget: %v", context.Cause(ctx))
		}
		budget.suspend()()
		if ctx.Err() == nil {
			t.Fatal("entering a queue resurrected an expired budget")
		}
	})
}

func TestKernelRecoveryPreparationBudgetRetiresOnlyItsOwnTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancelParent := context.WithCancelCause(context.Background())
		defer cancelParent(nil)
		ctx, closeBudget := withKernelRecoveryPreparationBudget(parent, 100*time.Millisecond)
		defer closeBudget()
		time.Sleep(40 * time.Millisecond)
		retireKernelRecoveryPreparation(ctx)
		time.Sleep(time.Hour)
		if ctx.Err() != nil || kernelRecoveryPreparationActive(ctx) {
			t.Fatal("dispatched execution still owns a preparation timer")
		}
		cause := errors.New("caller lease lost")
		cancelParent(cause)
		if !errors.Is(context.Cause(ctx), cause) {
			t.Fatalf("retirement severed the original caller: %v", context.Cause(ctx))
		}
	})
}

func TestKernelRecoveryPreparationQueueRetainsCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancelParent := context.WithTimeoutCause(context.Background(), 80*time.Millisecond, context.DeadlineExceeded)
		defer cancelParent()
		ctx, closeBudget := withKernelRecoveryPreparationBudget(parent, time.Second)
		defer closeBudget()
		resume := kernelRecoveryPreparationBudgetFrom(ctx).suspend()
		defer resume()
		time.Sleep(81 * time.Millisecond)
		if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) || errors.Is(context.Cause(ctx), errSessionRunnerKernelRecoveryDeadline) {
			t.Fatalf("queue extended or relabeled caller deadline: %v", context.Cause(ctx))
		}
	})
}

func TestKernelRecoveryPreparationCommandHasRemainingBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, closeBudget := withKernelRecoveryPreparationBudget(context.Background(), 100*time.Millisecond)
		defer closeBudget()
		time.Sleep(30 * time.Millisecond)
		setup, cancelSetup := detachedKernelSetupContext(ctx, 0)
		defer cancelSetup()
		command, cancelCommand := kernelRecoveryPreparationCommandContext(setup)
		defer cancelCommand()
		deadline, found := command.Deadline()
		if !found || time.Until(deadline) != 70*time.Millisecond {
			t.Fatalf("dispatch RPC lost the remaining preparation bound: found=%t remaining=%s", found, time.Until(deadline))
		}
		time.Sleep(71 * time.Millisecond)
		synctest.Wait()
		if command.Err() == nil || setup.Err() == nil || !errors.Is(context.Cause(ctx), errSessionRunnerKernelRecoveryDeadline) {
			t.Fatal("unhanded preparation did not stop when its budget expired")
		}
	})
}
