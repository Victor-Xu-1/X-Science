package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const managedEndpointDockerInspectProjection = `{"Id":{{json .Id}},"Config":{"Labels":{"ai.x-science.endpoint-instance":{{json (index .Config.Labels "ai.x-science.endpoint-instance")}},"ai.x-science.endpoint-user":{{json (index .Config.Labels "ai.x-science.endpoint-user")}},"ai.x-science.endpoint-approval":{{json (index .Config.Labels "ai.x-science.endpoint-approval")}}}},"State":{"Status":{{json .State.Status}},"Running":{{json .State.Running}},"Restarting":{{json .State.Restarting}}},"HostConfig":{"LogConfig":{"Type":{{json .HostConfig.LogConfig.Type}},"Config":{"max-size":{{json (index .HostConfig.LogConfig.Config "max-size")}},"max-file":{{json (index .HostConfig.LogConfig.Config "max-file")}}}},"NetworkSettings":{"Ports":{{json .NetworkSettings.Ports}}}}`

type managedEndpointDocker struct{ executable string }

type managedEndpointDockerInspection struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Restarting bool   `json:"Restarting"`
	} `json:"State"`
	HostConfig struct {
		LogConfig struct {
			Type   string            `json:"Type"`
			Config map[string]string `json:"Config"`
		} `json:"LogConfig"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func (runtime *managedEndpointDocker) validateNamedOwner(ctx context.Context, instance ManagedEndpointOwnerInstance, allowNew bool) error {
	spec := instance.Registration.Owner.Docker
	if spec == nil {
		return ErrManagedEndpointOwnershipUnknown
	}
	output, err := runtime.command(ctx, *spec, "container", "ls", "--all", "--no-trunc", "--filter", "name=^/"+regexp.QuoteMeta(spec.Container)+"$", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		if allowNew && instance.Identity == (ManagedEndpointRuntimeIdentity{}) && instance.Registration.Owner.Adopt == nil {
			return nil
		}
		return errors.New("approved Docker locator no longer identifies the original endpoint owner")
	}
	if len(ids) != 1 {
		return ErrManagedEndpointOwnershipUnknown
	}
	identity, _, err := runtime.inspect(ctx, instance)
	if err != nil {
		return err
	}
	if identity.ContainerID != ids[0] {
		return errors.New("approved Docker script locator was rebound to another container")
	}
	return nil
}

func newManagedEndpointDocker() (*managedEndpointDocker, error) {
	executable, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("Docker runtime is unavailable for the approved endpoint owner")
	}
	return &managedEndpointDocker{executable: executable}, nil
}

func (runtime *managedEndpointDocker) command(ctx context.Context, spec ManagedEndpointDockerSpec, args ...string) (string, error) {
	if !validManagedOwnerComponent(spec.Context) {
		return "", errors.New("invalid endpoint Docker context")
	}
	output, err := managedEndpointControlCommand(ctx, runtime.executable, append([]string{"--context=" + spec.Context}, args...)...)
	if err != nil {
		return "", fmt.Errorf("observe approved Docker endpoint: %w", err)
	}
	return output, nil
}

func (runtime *managedEndpointDocker) inspect(ctx context.Context, instance ManagedEndpointOwnerInstance) (ManagedEndpointRuntimeIdentity, managedEndpointDockerInspection, error) {
	var state managedEndpointDockerInspection
	spec := instance.Registration.Owner.Docker
	if spec == nil {
		return ManagedEndpointRuntimeIdentity{}, state, errors.New("Docker owner contract is missing")
	}
	daemon, err := runtime.command(ctx, *spec, "info", "--format", "{{.ID}}")
	if err != nil {
		return ManagedEndpointRuntimeIdentity{}, state, err
	}
	locator := spec.Container
	if instance.Identity.ContainerID != "" {
		locator = instance.Identity.ContainerID
	} else if adopt := instance.Registration.Owner.Adopt; adopt != nil {
		locator = adopt.ContainerID
	}
	// Project only owner facts. Config.Env, health-check output, mounts and
	// unrelated labels are not read as a side effect of lifecycle observation.
	raw, err := runtime.command(ctx, *spec, "container", "inspect", "--format", managedEndpointDockerInspectProjection, "--", locator)
	if err != nil {
		return ManagedEndpointRuntimeIdentity{}, state, err
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ManagedEndpointRuntimeIdentity{}, state, errors.New("Docker endpoint inspection is malformed or incomplete")
	}
	identity := ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: strings.TrimSpace(daemon), ContainerID: state.ID}
	if err := identity.ValidateFor(instance); err != nil {
		return identity, state, err
	}
	if adopt := instance.Registration.Owner.Adopt; adopt != nil {
		if identity != *adopt {
			return identity, state, errors.New("Docker endpoint adoption identity no longer matches its approval")
		}
	} else if instance.Identity == (ManagedEndpointRuntimeIdentity{}) {
		labels := state.Config.Labels
		if labels["ai.x-science.endpoint-instance"] != instance.ID || labels["ai.x-science.endpoint-user"] != instance.UserID || labels["ai.x-science.endpoint-approval"] != instance.ApprovedHash {
			return identity, state, errors.New("Docker endpoint has no positive instance ownership labels")
		}
	}
	return identity, state, nil
}

func validateManagedEndpointDockerLogging(state managedEndpointDockerInspection) error {
	config := state.HostConfig.LogConfig
	if config.Type != "local" && config.Type != "json-file" {
		return errors.New("Docker endpoint logging has no supported bounded retention proof")
	}
	size := strings.TrimSpace(config.Config["max-size"])
	if size == "" {
		return errors.New("Docker endpoint logging max-size is not positively configured")
	}
	multiplier := int64(1)
	last := size[len(size)-1]
	switch last {
	case 'k', 'K':
		multiplier = 1024
		size = size[:len(size)-1]
	case 'm', 'M':
		multiplier = 1024 * 1024
		size = size[:len(size)-1]
	case 'g', 'G':
		multiplier = 1024 * 1024 * 1024
		size = size[:len(size)-1]
	}
	number, err := strconv.ParseInt(size, 10, 64)
	if err != nil || number < 1 || number > (1<<40)/multiplier {
		return errors.New("Docker endpoint log max-size is not a finite supported byte count")
	}
	files, err := strconv.ParseInt(config.Config["max-file"], 10, 32)
	if err != nil || files < 1 || files > 1024 {
		return errors.New("Docker endpoint log max-file is not positively bounded")
	}
	return nil
}

func managedEndpointDockerPortMatches(registration ManagedEndpointRegistration, state managedEndpointDockerInspection) bool {
	parsed, err := url.Parse(registration.URL)
	if err != nil || parsed.Hostname() != "127.0.0.1" {
		return false
	}
	for _, bindings := range state.NetworkSettings.Ports {
		for _, binding := range bindings {
			if binding.HostIP == "127.0.0.1" && binding.HostPort == parsed.Port() {
				return true
			}
		}
	}
	return false
}

func (runtime *managedEndpointDocker) observeReady(ctx context.Context, instance ManagedEndpointOwnerInstance) (ManagedEndpointRuntimeIdentity, error) {
	identity, state, err := runtime.inspect(ctx, instance)
	if err != nil {
		return identity, err
	}
	if err := validateManagedEndpointDockerLogging(state); err != nil {
		return identity, err
	}
	if !state.State.Running || state.State.Restarting || !managedEndpointDockerPortMatches(instance.Registration, state) {
		return identity, errors.New("Docker endpoint owner is not running with the approved loopback binding")
	}
	return identity, nil
}

func (runtime *managedEndpointDocker) observeStopped(ctx context.Context, instance ManagedEndpointOwnerInstance) (*ManagedEndpointStoppedProof, error) {
	identity, state, err := runtime.inspect(ctx, instance)
	if err != nil {
		// Absence is proved by a successful typed enumeration against the same
		// daemon and full immutable ID, never by matching stderr text.
		expected := instance.Identity
		if expected == (ManagedEndpointRuntimeIdentity{}) && instance.Registration.Owner.Adopt != nil {
			expected = *instance.Registration.Owner.Adopt
		}
		if expected.Kind != "docker" {
			return nil, err
		}
		spec := *instance.Registration.Owner.Docker
		daemon, daemonErr := runtime.command(ctx, spec, "info", "--format", "{{.ID}}")
		if daemonErr != nil || strings.TrimSpace(daemon) != expected.DaemonID {
			return nil, errors.Join(err, daemonErr)
		}
		ids, listErr := runtime.command(ctx, spec, "container", "ls", "--all", "--no-trunc", "--filter", "id="+expected.ContainerID, "--format", "{{.ID}}")
		if listErr != nil || strings.TrimSpace(ids) != "" {
			return nil, errors.Join(err, listErr)
		}
		proof := &ManagedEndpointStoppedProof{Identity: expected, State: "absent", ControlReachable: true, ObservedAt: time.Now().UTC()}
		return proof, proof.ValidateFor(instance)
	}
	if state.State.Running || state.State.Restarting || (state.State.Status != "exited" && state.State.Status != "dead") {
		return nil, errors.New("Docker endpoint original container has not stopped")
	}
	proof := &ManagedEndpointStoppedProof{Identity: identity, State: state.State.Status, ControlReachable: true, ObservedAt: time.Now().UTC()}
	return proof, proof.ValidateFor(instance)
}
