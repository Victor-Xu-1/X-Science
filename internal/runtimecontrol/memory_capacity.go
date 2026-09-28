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
	return decodeMemory.reserve(bytes, availableDecodeMemory)
}

func (r *memoryReservations) reserve(bytes uint64, available func() uint64) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	headroom := available()
	// Leave half of observed headroom for request handling, GC and other work.
	budget := headroom / 2
	if r.reserved > budget || bytes > budget-r.reserved {
		return nil, ErrInsufficientMemory
	}
	r.reserved += bytes
	var once sync.Once
	return func() { once.Do(func() { r.mu.Lock(); r.reserved -= bytes; r.mu.Unlock() }) }, nil
}

func availableDecodeMemory() uint64 {
	available := platformAvailableMemory()
	limit := debug.SetMemoryLimit(-1) // Query only; never changes the runtime limit.
	if limit >= 0 && limit < math.MaxInt64 {
		var usage runtime.MemStats
		runtime.ReadMemStats(&usage)
		used := usage.Sys - usage.HeapReleased
		remaining := uint64(0)
		if used < uint64(limit) {
			remaining = uint64(limit) - used
		}
		available = min(available, remaining)
	}
	return available
}
