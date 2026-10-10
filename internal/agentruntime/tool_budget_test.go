package agentruntime

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestToolRoundBudgetRemainingTracksConsumption(t *testing.T) {
	budget := NewToolRoundBudget(3)

	if remaining, bounded := budget.Remaining(); !bounded || remaining != 3 {
		t.Fatalf("fresh Remaining = (%d, %t), want (3, true)", remaining, bounded)
	}
	for used := 1; used <= 3; used++ {
		if err := budget.consume(); err != nil {
			t.Fatalf("consume %d of 3 returned error %v", used, err)
		}
		if remaining, bounded := budget.Remaining(); !bounded || remaining != 3-used {
			t.Fatalf("after %d consumes Remaining = (%d, %t), want (%d, true)", used, remaining, bounded, 3-used)
		}
	}
	if err := budget.consume(); !errors.As(err, new(*ToolRoundLimitError)) {
		t.Fatalf("consume past limit returned %v, want ToolRoundLimitError", err)
	}
	if remaining, _ := budget.Remaining(); remaining != 0 {
		t.Fatalf("exhausted Remaining = %d, want 0", remaining)
	}
}

func TestToolRoundLimitErrorMentionsLimit(t *testing.T) {
	err := &ToolRoundLimitError{Limit: 4}
	if err.Error() != "agent runtime exceeded 4 tool call rounds" {
		t.Fatalf("ToolRoundLimitError message = %q", err.Error())
	}
}

func TestToolRoundBudgetUnlimitedLimitsDoNotBoundRounds(t *testing.T) {
	for _, limit := range []int{0, -5} {
		budget := NewToolRoundBudget(limit)
		if _, bounded := budget.Remaining(); bounded {
			t.Fatalf("limit %d reported a bounded Remaining", limit)
		}
		for range 1000 {
			if err := budget.consume(); err != nil {
				t.Fatalf("limit %d returned error %v; non-positive limits are unlimited", limit, err)
			}
		}
	}
}

func TestToolRoundBudgetNilReceiverIsUnlimited(t *testing.T) {
	var budget *ToolRoundBudget
	if err := budget.consume(); err != nil {
		t.Fatalf("nil budget consume returned error %v", err)
	}
	if remaining, bounded := budget.Remaining(); bounded || remaining != 0 {
		t.Fatalf("nil budget Remaining = (%d, %t), want (0, false)", remaining, bounded)
	}
}

func TestToolRoundBudgetIsConcurrencySafe(t *testing.T) {
	const workers = 16
	const perWorker = 50
	budget := NewToolRoundBudget(workers * perWorker)

	var wg sync.WaitGroup
	var consumeErrs atomic.Int32
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWorker {
				if err := budget.consume(); err != nil {
					consumeErrs.Add(1)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := consumeErrs.Load(); got != 0 {
		t.Fatalf("consume under exact-fit concurrency returned %d errors", got)
	}
	if remaining, bounded := budget.Remaining(); !bounded || remaining != 0 {
		t.Fatalf("Remaining after full concurrent consumption = (%d, %t), want (0, true)", remaining, bounded)
	}
}

func TestToolRoundBudgetNeverOvershootsUnderContention(t *testing.T) {
	const limit = 8
	const workers = 32
	budget := NewToolRoundBudget(limit)

	var wg sync.WaitGroup
	var accepted atomic.Int32
	var rejected atomic.Int32
	var unexpected atomic.Int32
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := budget.consume(); err != nil {
				var limitErr *ToolRoundLimitError
				if !errors.As(err, &limitErr) || limitErr.Limit != limit {
					unexpected.Add(1)
					return
				}
				rejected.Add(1)
				return
			}
			accepted.Add(1)
		}()
	}
	wg.Wait()
	if unexpected.Load() != 0 {
		t.Fatalf("unexpected consume errors = %d", unexpected.Load())
	}
	if accepted.Load() != limit {
		t.Fatalf("accepted consumes = %d, want exactly the limit %d", accepted.Load(), limit)
	}
	if rejected.Load() != workers-limit {
		t.Fatalf("rejected consumes = %d, want %d", rejected.Load(), workers-limit)
	}
	if remaining, _ := budget.Remaining(); remaining != 0 {
		t.Fatalf("Remaining after oversubscribed consume = %d, want 0", remaining)
	}
}
