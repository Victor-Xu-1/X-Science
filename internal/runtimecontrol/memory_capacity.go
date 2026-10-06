package runtimecontrol

import (
	"errors"
	"math"
	"runtime"
	"runtime/debug"
	"sync"
)

var ErrInsufficientMemory = errors.New("insufficient available memory to decode response; reduce the requested result page or retry after releasing memory")

var decodeMemory = memoryReservations{}

type memoryReservations struct {
	mu       sync.Mutex
	reserved uint64
}

// ReserveDecodeMemory admits materialization, not transfer. The byte estimate
// covers decoder buffers and copied result representations. Reservations prevent
// concurrent decoders from each spending the same measured headroom. This is a
// conservative admission check, not a promise against unrelated future writers.
func ReserveDecodeMemory(bytes uint64) (func(), error) {
	return decodeMemory.reserve(bytes, availableDecodeBudget)
}

func (r *memoryReservations) reserve(bytes uint64, availableBudget func() uint64) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	budget := availableBudget()
	if r.reserved > budget || bytes > budget-r.reserved {
		return nil, ErrInsufficientMemory
	}
	r.reserved += bytes
	var once sync.Once
	return func() { once.Do(func() { r.mu.Lock(); r.reserved -= bytes; r.mu.Unlock() }) }, nil
}

func availableDecodeBudget() uint64 {
	limit := debug.SetMemoryLimit(-1) // Query only; never changes the runtime limit.
	var usage runtime.MemStats
	runtime.ReadMemStats(&usage)
	return decodeAdmissionBudget(platformAvailableMemory(), limit, usage.Sys-usage.HeapReleased)
}

func decodeAdmissionBudget(physicalHeadroom uint64, runtimeLimit int64, runtimeUsed uint64) uint64 {
	// Keep half of the physical headroom for control, GC and unrelated work.
	// The decoder's conservative footprint is then bounded by the remaining
	// runtime budget, not halved again as though it were fresh physical space.
	budget := physicalHeadroom / 2
	if runtimeLimit >= 0 && runtimeLimit < math.MaxInt64 {
		remaining := uint64(0)
		if runtimeUsed < uint64(runtimeLimit) {
			remaining = uint64(runtimeLimit) - runtimeUsed
		}
		budget = min(budget, remaining)
	}
	return budget
}
