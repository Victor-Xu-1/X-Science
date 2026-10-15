//go:build linux

package compute

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type managedEndpointUnit struct {
	run, control string
	// The adapter reads only the already established cgroup identity. Keeping
	// the reader here also allows contradictory control/cgroup evidence to be
	// tested without touching any actual service or host cgroup.
	cgroupPopulated func(string) (bool, error)
}

func (unit *managedEndpointUnit) populated(group string) (bool, error) {
	if unit.cgroupPopulated != nil {
		return unit.cgroupPopulated(group)
	}
	return managedEndpointCgroupPopulated(group)
}

func newManagedEndpointUnit() (*managedEndpointUnit, error) {
	run, err := exec.LookPath("systemd-run")
	if err != nil {
		return nil, errors.New("systemd user service ownership is required for local managed endpoints")
	}
	control, err := exec.LookPath("systemctl")
	if err != nil {
		return nil, errors.New("systemctl is required to observe the managed endpoint owner")
	}
	return &managedEndpointUnit{run: run, control: control}, nil
}

func managedEndpointInstanceDirectory(instance ManagedEndpointOwnerInstance) string {
	return filepath.Join(instance.RuntimeRoot, instance.ID)
}

func managedEndpointSocketPath(instance ManagedEndpointOwnerInstance) (string, error) {
	runtimeRoot := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtimeRoot) {
		return "", errors.New("XDG_RUNTIME_DIR is required for private endpoint control")
	}
	sum := sha256.Sum256([]byte(instance.RuntimeRoot))
	directory := filepath.Join(runtimeRoot, "x-science-endpoints", hex.EncodeToString(sum[:8]))
	if err := ensureManagedEndpointPrivateDirectory(directory); err != nil {
		return "", err
	}
	path := filepath.Join(directory, instance.ID+".sock")
	if len(path) >= 108 {
		return "", errors.New("managed endpoint control socket path exceeds the platform address limit")
	}
	return path, nil
}

func ensureManagedEndpointPrivateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("managed endpoint runtime path is not absolute")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("managed endpoint runtime directory is not private")
	}
	return nil
}

func (unit *managedEndpointUnit) launch(ctx context.Context, instance ManagedEndpointOwnerInstance, executable string) error {
	if err := instance.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(executable) {
		return errors.New("managed endpoint worker executable must be absolute")
	}
	dir := managedEndpointInstanceDirectory(instance)
	if err := ensureManagedEndpointPrivateDirectory(dir); err != nil {
		return err
	}
	descriptor := filepath.Join(dir, "owner.json")
	var existing ManagedEndpointOwnerInstance
	if err := readManagedEndpointPrivateJSON(descriptor, &existing); err == nil {
		if existing.ID != instance.ID || existing.UserID != instance.UserID || existing.Endpoint != instance.Endpoint || existing.ApprovedHash != instance.ApprovedHash || existing.StartGeneration != instance.StartGeneration || existing.RuntimeRoot != instance.RuntimeRoot || existing.ServiceDir != instance.ServiceDir || existing.WorkspacePath != instance.WorkspacePath || existing.InitialAction != instance.InitialAction {
			return ErrManagedEndpointCorruptOwner
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeManagedEndpointPrivateJSON(descriptor, instance); err != nil {
			return err
		}
	} else {
		return err
	}
	arguments := []string{
		"--user", "--quiet", "--collect", "--service-type=exec", "--unit=" + ManagedEndpointUnitName(instance.ID),
		"--property=Restart=no", "--property=KillMode=control-group", "--property=TimeoutStopSec=10s",
		"--property=UMask=0077", "--property=StandardOutput=null", "--property=StandardError=null",
	}
	for _, value := range managedEndpointBaseEnvironment() {
		arguments = append(arguments, "--setenv="+value)
	}
	arguments = append(arguments, "--", executable, "managed-endpoint-owner", "--descriptor", descriptor)
	_, err := managedEndpointControlCommand(ctx, unit.run, arguments...)
	return err
}

type managedEndpointUnitObservation struct {
	Identity ManagedEndpointRuntimeIdentity
	State    string
	Absent   bool
	Empty    bool
}

func (unit *managedEndpointUnit) observe(ctx context.Context, instance ManagedEndpointOwnerInstance) (managedEndpointUnitObservation, error) {
	name := ManagedEndpointUnitName(instance.ID)
	raw, controlErr := managedEndpointControlCommand(ctx, unit.control, "--user", "show", "--property=LoadState,ActiveState,SubState,InvocationID,ControlGroup,MainPID", "--", name)
	values := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	observation := managedEndpointUnitObservation{State: values["ActiveState"]}
	expectedWorker := instance.WorkerIdentity
	if instance.Identity.Kind == "native-unit" {
		expectedWorker = instance.Identity
	}
	if values["LoadState"] == "not-found" && values["ActiveState"] == "inactive" {
		observation.Absent = true
		observation.Empty = true
		if expectedWorker.Kind == "native-unit" && expectedWorker.Unit == name {
			if populated, err := unit.populated(expectedWorker.ControlGroup); err == nil {
				observation.Empty = !populated
			} else if !errors.Is(err, os.ErrNotExist) {
				return observation, err
			}
			observation.Identity = expectedWorker
		}
		return observation, nil
	}
	if controlErr != nil {
		return observation, fmt.Errorf("managed endpoint unit control is unreachable: %w", controlErr)
	}
	if values["LoadState"] != "loaded" || (observation.State != "active" && observation.State != "activating" && observation.State != "deactivating" && observation.State != "inactive" && observation.State != "failed") {
		return observation, errors.New("managed endpoint unit state is unrecognized")
	}
	if observation.State == "inactive" || observation.State == "failed" {
		if expectedWorker.Kind != "native-unit" {
			return observation, nil
		}
		if values["InvocationID"] != "" && values["InvocationID"] != expectedWorker.InvocationID {
			return observation, errors.New("managed endpoint unit incarnation changed")
		}
		observation.Identity = expectedWorker
		populated, err := unit.populated(expectedWorker.ControlGroup)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return observation, err
		}
		observation.Empty = errors.Is(err, os.ErrNotExist) || !populated
		return observation, nil
	}
	pid, err := strconv.ParseInt(values["MainPID"], 10, 64)
	if err != nil || pid < 1 {
		return observation, errors.New("managed endpoint unit has no confirmed worker process")
	}
	ticks, state, err := managedEndpointProcessTicks(pid)
	if err != nil || state == "Z" || state == "X" {
		return observation, errors.New("managed endpoint worker process incarnation is not live")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return observation, err
	}
	observation.Identity = ManagedEndpointRuntimeIdentity{Kind: "native-unit", BootID: strings.TrimSpace(string(boot)), Unit: name, InvocationID: values["InvocationID"], ControlGroup: values["ControlGroup"], PID: pid, StartTicks: ticks}
	if err := validateManagedEndpointPhysicalIdentity(observation.Identity); err != nil {
		return observation, err
	}
	if instance.Identity.Kind == "native-unit" && instance.Identity != observation.Identity {
		return observation, errors.New("managed endpoint original worker identity changed")
	}
	return observation, nil
}

func managedEndpointProcessTicks(pid int64) (uint64, string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, "", err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, "", errors.New("managed endpoint process stat is malformed")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return 0, "", errors.New("managed endpoint process stat is incomplete")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	return ticks, fields[0], err
}

func managedEndpointCgroupPopulated(group string) (bool, error) {
	if !strings.HasPrefix(group, "/") || group == "/" || filepath.Clean(group) != group {
		return false, errors.New("managed endpoint cgroup identity is invalid")
	}
	raw, err := os.ReadFile("/sys/fs/cgroup" + group + "/cgroup.events")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "populated 0" {
			return false, nil
		}
		if line == "populated 1" {
			return true, nil
		}
	}
	return false, errors.New("managed endpoint cgroup population is unknown")
}

func (unit *managedEndpointUnit) stop(ctx context.Context, instance ManagedEndpointOwnerInstance) (*ManagedEndpointStoppedProof, error) {
	before, err := unit.observe(ctx, instance)
	if err != nil {
		return nil, err
	}
	if before.Absent && !before.Empty {
		// A missing unit name is not proof that its original cgroup exited.
		// Do not retire this owner or address a possibly reused unit by name.
		return nil, errors.New("managed endpoint unit is absent but its original cgroup is still populated")
	}
	if before.Empty {
		state := before.State
		if before.Absent {
			state = "absent"
		}
		proof := &ManagedEndpointStoppedProof{Identity: before.Identity, ObservedAt: time.Now().UTC(), State: state, ControlReachable: true, CgroupEmpty: before.Empty}
		return proof, proof.ValidateFor(instance)
	}
	if instance.Identity.Kind != "native-unit" || before.Identity != instance.Identity {
		return nil, ErrManagedEndpointOwnershipUnknown
	}
	if _, err := managedEndpointControlCommand(ctx, unit.control, "--user", "stop", "--", instance.Identity.Unit); err != nil {
		return nil, err
	}
	after, err := unit.observe(ctx, instance)
	if err != nil {
		return nil, err
	}
	proof := &ManagedEndpointStoppedProof{Identity: instance.Identity, ObservedAt: time.Now().UTC(), State: after.State, ControlReachable: true, CgroupEmpty: after.Empty}
	if after.Absent {
		proof.State = "absent"
	}
	return proof, proof.ValidateFor(instance)
}
