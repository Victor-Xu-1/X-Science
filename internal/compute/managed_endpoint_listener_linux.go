//go:build linux

package compute

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A healthy port is not proof that this owner started it. Resolve the kernel's
// actual listening socket inode and require a live, incarnation-checked member
// of the exact owned cgroup, including nested groups and setsid descendants.
func verifyManagedEndpointNativeListener(ctx context.Context, instance ManagedEndpointOwnerInstance, identity ManagedEndpointRuntimeIdentity) error {
	if err := identity.ValidateFor(instance); err != nil {
		return err
	}
	file, err := os.Open("/proc/net/tcp")
	if err != nil {
		return err
	}
	defer file.Close()
	wanted := fmt.Sprintf("0100007F:%04X", instance.Registration.Port)
	inodes := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 10 && fields[1] == wanted && fields[3] == "0A" {
			inodes[fields[9]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(inodes) == 0 {
		return errors.New("approved native endpoint has no literal loopback listening socket")
	}
	groupRoot := "/sys/fs/cgroup" + identity.ControlGroup
	found := false
	err = filepath.WalkDir(groupRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if found {
			return fs.SkipAll
		}
		if entry.IsDir() || entry.Name() != "cgroup.procs" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range strings.Fields(string(raw)) {
			pid, err := strconv.ParseInt(value, 10, 64)
			if err != nil || pid < 1 {
				return errors.New("native endpoint cgroup process identity is malformed")
			}
			ticks, state, err := managedEndpointProcessTicks(pid)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if state == "Z" || state == "X" {
				continue
			}
			entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			for _, entry := range entries {
				link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, entry.Name()))
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") || !inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
					continue
				}
				after, afterState, err := managedEndpointProcessTicks(pid)
				if err != nil || after != ticks || afterState == "Z" || afterState == "X" {
					continue
				}
				membership, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
				if err != nil {
					return err
				}
				for _, line := range strings.Split(string(membership), "\n") {
					group, ok := strings.CutPrefix(line, "0::")
					if ok && (group == identity.ControlGroup || strings.HasPrefix(group, identity.ControlGroup+"/")) {
						found = true
						return fs.SkipAll
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return errors.New("readiness listener is not owned by this endpoint instance")
	}
	return nil
}
