//go:build !linux && !windows

package runtimecontrol

// Without platform capacity telemetry, retain a conservative materialization
// budget instead of silently allowing unbounded allocations. The runtime heap
// limit, when configured, can still tighten this fallback.
func platformAvailableMemory() uint64 { return 1 << 30 }
