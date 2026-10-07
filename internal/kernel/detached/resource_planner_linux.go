//go:build linux

package detached

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	workspace "synon-go/internal/persistence/workspace"
)

type executorResourcePlanner interface {
	ResourceCapacity(context.Context, int64) (workspace.KernelResourceCapacity, int64, error)
	ResourceUsage(context.Context, string, int64) (int64, ExecutorLaunchState, error)
}

func (l *SystemdUserExecutorLauncher) ResourceCapacity(ctx context.Context, requested int64) (workspace.KernelResourceCapacity, int64, error) {
	total, available, reserve, err := readExecutorMemory(l.meminfoPath)
	if err != nil {
		return workspace.KernelResourceCapacity{}, 0, err
	}
	// The conservative default share is not a ceiling on an explicit budget.
	// Large hosts retain the fixed controller headroom plus all current usage,
	// rather than withholding forty percent of their RAM from declared work.
	if requested > 0 {
		reserve = executorControlReserveBytes
	}
	return workspace.KernelResourceCapacity{MaximumBytes: max(0, total-reserve), AvailableBytes: max(0, available-reserve), DefaultBytes: max(executorMemoryFloorBytes, memoryFraction(total, executorMemoryTotalFraction)), MinimumBytes: executorMemoryFloorBytes, Usage: map[string]int64{}}, reserve, nil
}

func (l *SystemdUserExecutorLauncher) ResourceUsage(ctx context.Context, id string, generation int64) (int64, ExecutorLaunchState, error) {
	if !validExecutorUnitComponent(id) || generation < 1 || !strings.HasPrefix(l.systemctl, "/") {
		return 0, ExecutorLaunchUnknown, errors.New("resource observer authority invalid")
	}
	window, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(window, l.systemctl, "--user", "show", "--property=LoadState,ActiveState,ControlGroup", executorUnitName(id, generation)).Output()
	fields := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	if fields["LoadState"] == "not-found" && fields["ActiveState"] == "inactive" {
		return 0, ExecutorLaunchExited, nil
	}
	if err != nil {
		return 0, ExecutorLaunchUnknown, err
	}
	if fields["ActiveState"] == "inactive" || fields["ActiveState"] == "failed" {
		return 0, ExecutorLaunchExited, nil
	}
	if fields["ActiveState"] != "active" && fields["ActiveState"] != "activating" && fields["ActiveState"] != "deactivating" {
		return 0, ExecutorLaunchUnknown, nil
	}
	// MemoryCurrent includes reclaimable file cache, which MemAvailable may
	// already count as available. Subtracting it from the outstanding promise
	// would double-discount that cache and over-admit. Only resident anonymous
	// usage is credited here; unknown/kernel/shmem usage stays conservative.
	group := fields["ControlGroup"]
	if group == "" || group == "/" || filepath.Clean(group) != group || !filepath.IsAbs(group) {
		return 0, ExecutorLaunchAlive, nil
	}
	stats, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(group, "/"), "memory.stat"))
	if err != nil {
		return 0, ExecutorLaunchAlive, nil
	}
	used := int64(0)
	for _, line := range strings.Split(string(stats), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "anon" {
			value, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil && value >= 0 {
				used = value
			}
			break
		}
	}
	return used, ExecutorLaunchAlive, nil
}

func readExecutorMemory(location string) (total, available, reserve int64, err error) {
	hasAvailable := false
	if location == "" {
		location = "/proc/meminfo"
	}
	raw, err := os.ReadFile(location)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		value, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil || value < 0 || value > (1<<62)/1024 {
			return 0, 0, 0, errors.New("machine memory observation invalid")
		}
		if fields[0] == "MemTotal:" {
			total = value * 1024
		} else {
			available = value * 1024
			hasAvailable = true
		}
	}
	if total <= 0 || !hasAvailable || available < 0 || available > total {
		return 0, 0, 0, errors.New("machine memory observation incomplete")
	}
	reserve = max(executorControlReserveBytes, memoryFraction(total, executorMemoryReserveFraction))
	return total, available, reserve, nil
}

func memoryFraction(bytes, percent int64) int64 { return bytes/100*percent + bytes%100*percent/100 }

func (b *Backend) reserveExecutorResources(ctx context.Context, backend workspace.KernelExecutionBackend) (int64, error) {
	planner, ok := b.Launcher.(executorResourcePlanner)
	if !ok {
		return 0, nil
	}
	spec, err := workspace.DecodeKernelExecutionSessionSpecV1(backend.SessionSpecJSON)
	if err != nil {
		return 0, err
	}
	capacity, reserve, err := planner.ResourceCapacity(ctx, spec.ResourceMemoryBytes)
	if err != nil {
		return 0, err
	}
	reservations, err := b.Store.ListKernelResourceReservations(ctx, backend.MachineBootID)
	if err != nil {
		return 0, err
	}
	for _, item := range reservations {
		if item.State != "reserved" {
			continue
		}
		usage, state, observeErr := planner.ResourceUsage(ctx, item.BackendID, item.Generation)
		if observeErr != nil || state == ExecutorLaunchUnknown {
			continue
		}
		if state == ExecutorLaunchExited {
			// A reservation can precede launch. The current generation must not
			// release itself in the gap before its systemd request is dispatched.
			if item.BackendID == backend.BackendID && item.Generation == backend.BackendGeneration {
				continue
			}
			current, found, err := b.Store.GetKernelExecutionBackend(ctx, item.BackendID)
			if err != nil {
				return 0, err
			}
			if found && current.BackendGeneration == item.Generation && current.State == workspace.KernelExecutionBackendStateStarting {
				continue
			}
			if err := b.Store.ReleaseKernelResources(ctx, item.BackendID, item.Generation, item.BootID); err != nil {
				return 0, err
			}
		} else {
			capacity.Usage[workspace.KernelResourceUsageKey(item.BackendID, item.Generation)] = usage
		}
	}
	reservation, admitted, err := b.Store.AdmitKernelResources(ctx, backend, spec.ResourceMemoryBytes, capacity)
	if err != nil {
		return 0, err
	}
	if !admitted {
		return 0, &ExecutorResourceUnavailableError{AvailableBytes: capacity.AvailableBytes + reserve, ReserveBytes: reserve, RequiredBytes: max(spec.ResourceMemoryBytes, capacity.MinimumBytes), BackendID: backend.BackendID, Generation: backend.BackendGeneration}
	}
	return reservation.ReservedBytes, nil
}
