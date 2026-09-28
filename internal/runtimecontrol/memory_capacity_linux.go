//go:build linux

package runtimecontrol

import (
	"errors"
	"os"
	"path"
	"strconv"
	"strings"
)

func platformAvailableMemory() uint64 {
	return linuxAvailableMemory("/proc", "/sys/fs/cgroup")
}

func linuxAvailableMemory(procRoot, cgroupRoot string) uint64 {
	raw, err := os.ReadFile(path.Join(procRoot, "meminfo"))
	if err != nil {
		return 0
	}
	available := uint64(0)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "MemAvailable:" && fields[2] == "kB" {
			value, err := strconv.ParseUint(fields[1], 10, 54)
			if err == nil {
				available = value * 1024
			}
		}
	}
	// Host free memory alone is insufficient inside a constrained service.
	// Every cgroup ancestor can impose a tighter limit than the leaf.
	membership, err := os.ReadFile(path.Join(procRoot, "self/cgroup"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(membership), "\n") {
		if !strings.HasPrefix(line, "0::/") {
			continue
		}
		group := strings.TrimPrefix(line, "0::")
		if path.Clean(group) != group {
			return 0
		}
		root, err := os.OpenRoot(cgroupRoot)
		if err != nil {
			return 0
		}
		defer root.Close()
		for {
			directory := strings.TrimPrefix(group, "/")
			maximum, err := root.ReadFile(path.Join(directory, "memory.max"))
			// The cgroup root has no memory.max; a subtree can also omit it
			// when the memory controller is not enabled. Its ancestors still
			// impose their limits. Permission/read failures are not absence.
			if errors.Is(err, os.ErrNotExist) {
				if group == "/" {
					break
				}
				group = path.Dir(group)
				continue
			}
			if err != nil {
				return 0
			}
			if text := strings.TrimSpace(string(maximum)); text != "max" {
				limit, err := strconv.ParseUint(text, 10, 64)
				if err != nil {
					return 0
				}
				current, err := root.ReadFile(path.Join(directory, "memory.current"))
				if err != nil {
					return 0
				}
				used, err := strconv.ParseUint(strings.TrimSpace(string(current)), 10, 64)
				if err != nil || used >= limit {
					return 0
				}
				available = min(available, limit-used)
			}
			if group == "/" {
				break
			}
			group = path.Dir(group)
		}
	}
	return available
}
