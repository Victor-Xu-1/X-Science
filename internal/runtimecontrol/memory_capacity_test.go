package runtimecontrol

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDecodeMemoryReservationsDoNotOversubscribe(t *testing.T) {
	var reservations memoryReservations
	available := func() uint64 { return 50 }
	release, err := reservations.reserve(40, available)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reservations.reserve(11, available); !errors.Is(err, ErrInsufficientMemory) {
		t.Fatalf("oversubscribed: %v", err)
	}
	release()
	release()
	release, err = reservations.reserve(50, available)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := reservations.reserve(^uint64(0), available); !errors.Is(err, ErrInsufficientMemory) {
		t.Fatalf("overflow admitted: %v", err)
	}
	if _, err := reservations.reserve(1, func() uint64 { return 0 }); !errors.Is(err, ErrInsufficientMemory) {
		t.Fatalf("missing capacity admitted: %v", err)
	}
}

func TestDecodeMemoryConcurrentReservations(t *testing.T) {
	var reservations memoryReservations
	var accepted atomic.Int32
	var ready, finished sync.WaitGroup
	ready.Add(20)
	finished.Add(20)
	releaseAll := make(chan struct{})
	for range 20 {
		go func() {
			defer finished.Done()
			release, err := reservations.reserve(25, func() uint64 { return 50 })
			if err == nil {
				accepted.Add(1)
			}
			ready.Done()
			<-releaseAll
			if release != nil {
				release()
			}
		}()
	}
	ready.Wait()
	close(releaseAll)
	finished.Wait()
	if accepted.Load() != 2 || reservations.reserved != 0 {
		t.Fatalf("accepted=%d reserved=%d", accepted.Load(), reservations.reserved)
	}
}

func TestDecodeAdmissionSeparatesPhysicalReserveFromRuntimeBudget(t *testing.T) {
	const mib = uint64(1 << 20)
	for _, test := range []struct {
		name     string
		physical uint64
		limit    int64
		used     uint64
		want     uint64
	}{
		{"runtime_headroom_not_reserved_twice", 2 * 1024 * mib, int64(512 * mib), 64 * mib, 448 * mib},
		{"physical_reserve_retained", 128 * mib, int64(512 * mib), 64 * mib, 64 * mib},
		{"exhausted_runtime_refused", 2 * 1024 * mib, int64(64 * mib), 64 * mib, 0},
		{"unknown_physical_capacity_refused", 0, math.MaxInt64, 0, 0},
		{"unlimited_runtime_uses_physical_budget", 128 * mib, math.MaxInt64, 0, 64 * mib},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := decodeAdmissionBudget(test.physical, test.limit, test.used); got != test.want {
				t.Fatalf("budget=%d want=%d", got, test.want)
			}
		})
	}
}
