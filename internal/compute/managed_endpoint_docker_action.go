package compute

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// Once an instance exists, its lifecycle control is a typed operation on the
// immutable ID, not a second interpretation of an approved name-based script.
// Initial provisioning still owns its separate, once-admitted bootstrap.
func (runtime *managedEndpointDocker) controlOwned(ctx context.Context, instance ManagedEndpointOwnerInstance, kind string, output *os.File) managedEndpointActionExit {
	if runtime == nil || ctx == nil || output == nil || !filepath.IsAbs(runtime.executable) || (kind != "start" && kind != "stop") {
		return managedEndpointActionExit{Err: errors.New("invalid owned Docker control")}
	}
	if err := instance.Validate(); err != nil {
		return managedEndpointActionExit{Err: err}
	}
	expected := instance.Identity
	if expected == (ManagedEndpointRuntimeIdentity{}) && instance.Registration.Owner.Adopt != nil {
		expected = *instance.Registration.Owner.Adopt
	}
	if expected.Kind != "docker" {
		return managedEndpointActionExit{Err: ErrManagedEndpointOwnershipUnknown}
	}
	identity, _, err := runtime.inspect(ctx, instance)
	if err != nil {
		return managedEndpointActionExit{Err: err}
	}
	if identity != expected {
		return managedEndpointActionExit{Err: ErrManagedEndpointOwnershipUnknown}
	}
	spec := instance.Registration.Owner.Docker
	arguments := []string{"--context=" + spec.Context, "container", kind}
	arguments = append(arguments, "--", identity.ContainerID)
	command := exec.CommandContext(ctx, runtime.executable, arguments...)
	command.Env = managedEndpointBaseEnvironment()
	return runManagedEndpointOwnedCommand(ctx, command, output)
}
