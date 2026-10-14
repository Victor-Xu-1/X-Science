package server

import (
	"context"
	"sync"
	"time"
)

type kernelRecoveryPreparationBudgetKey struct{}

// This budget belongs to host preparation, not to an admitted execution. Only
// the real resource queue suspends it; durable dispatch retires it permanently.
type kernelRecoveryPreparationBudget struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelCauseFunc
	remaining time.Duration
	started   time.Time
	timer     *time.Timer
	epoch     uint64
	pauses    int
	retired   bool
}

func withKernelRecoveryPreparationBudget(parent context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	budget := &kernelRecoveryPreparationBudget{ctx: ctx, cancel: cancel, remaining: limit}
	budget.mu.Lock()
	budget.armLocked()
	budget.mu.Unlock()
	return context.WithValue(ctx, kernelRecoveryPreparationBudgetKey{}, budget), func() {
		budget.retire()
		cancel(nil)
	}
}

func kernelRecoveryPreparationBudgetFrom(ctx context.Context) *kernelRecoveryPreparationBudget {
	if ctx == nil {
		return nil
	}
	budget, _ := ctx.Value(kernelRecoveryPreparationBudgetKey{}).(*kernelRecoveryPreparationBudget)
	return budget
}

func (b *kernelRecoveryPreparationBudget) armLocked() {
	b.epoch++
	epoch := b.epoch
	b.started = time.Now()
	b.timer = time.AfterFunc(b.remaining, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if !b.retired && b.pauses == 0 && b.epoch == epoch {
			b.remaining = 0
			b.cancel(errSessionRunnerKernelRecoveryDeadline)
		}
	})
}

func (b *kernelRecoveryPreparationBudget) remainingLocked() time.Duration {
	if b.pauses == 0 {
		return max(0, b.remaining-time.Since(b.started))
	}
	return b.remaining
}

func (b *kernelRecoveryPreparationBudget) suspend() func() {
	b.mu.Lock()
	if b.retired || b.ctx.Err() != nil {
		b.mu.Unlock()
		return func() {}
	}
	if b.pauses == 0 {
		b.remaining = b.remainingLocked()
		if b.remaining <= 0 {
			b.cancel(errSessionRunnerKernelRecoveryDeadline)
			b.mu.Unlock()
			return func() {}
		}
		b.epoch++
		b.timer.Stop()
	}
	b.pauses++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.pauses--
			if b.pauses == 0 && !b.retired && b.ctx.Err() == nil {
				b.armLocked()
			}
		})
	}
}

func (b *kernelRecoveryPreparationBudget) retire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retired = true
	b.epoch++
	b.timer.Stop()
}

func kernelRecoveryPreparationActive(ctx context.Context) bool {
	budget := kernelRecoveryPreparationBudgetFrom(ctx)
	if budget == nil {
		return false
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return !budget.retired
}

func retireKernelRecoveryPreparation(ctx context.Context) {
	if budget := kernelRecoveryPreparationBudgetFrom(ctx); budget != nil {
		budget.retire()
	}
}

// A command RPC needs a fixed deadline even though resource waiting pauses the
// overall preparation timer. Never carry this command context into execution.
func kernelRecoveryPreparationCommandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := kernelRecoveryPreparationBudgetFrom(ctx)
	if budget == nil {
		return ctx, func() {}
	}
	budget.mu.Lock()
	remaining, retired := budget.remainingLocked(), budget.retired
	budget.mu.Unlock()
	if retired {
		return ctx, func() {}
	}
	deadline, cause := time.Now().Add(remaining), error(errSessionRunnerKernelRecoveryDeadline)
	if original, ok := budget.ctx.Deadline(); ok && original.Before(deadline) {
		deadline, cause = original, context.DeadlineExceeded
	}
	command, cancelCause := context.WithCancelCause(ctx)
	stop := context.AfterFunc(budget.ctx, func() { cancelCause(context.Cause(budget.ctx)) })
	if budget.ctx.Err() != nil {
		cancelCause(context.Cause(budget.ctx))
	}
	bounded, cancelDeadline := context.WithDeadlineCause(command, deadline, cause)
	return bounded, func() { stop(); cancelDeadline(); cancelCause(nil) }
}
