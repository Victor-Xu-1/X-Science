package runtimecontrol

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDecodeMemoryReservationsDoNotOversubscribe(t *testing.T) {
	var reservations memoryReservations
	available := func() uint64 { return 100 }
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
			release, err := reservations.reserve(25, func() uint64 { return 100 })
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
