package compute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"x-science/internal/subprocess"
)

type managedEndpointActionExit struct {
	Exited bool
	Code   *int
	Err    error
}

func managedEndpointBaseEnvironment() []string {
	result := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "NO_PROXY=localhost,127.0.0.1,127.0.0.0/8,::1", "no_proxy=localhost,127.0.0.1,127.0.0.0/8,::1"}
	// Docker contexts use the operator's normal home. Never inherit ambient
	// tokens, proxy settings, arbitrary loader hooks or the web environment.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		result = append(result, "HOME="+home)
	}
	if runtimeRoot := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtimeRoot) {
		result = append(result, "XDG_RUNTIME_DIR="+runtimeRoot)
	}
	return result
}

func managedEndpointActionEnvironment(instance ManagedEndpointOwnerInstance, values map[string]string) ([]string, error) {
	if values["HOST_PORT"] != strconv.Itoa(instance.Registration.Port) || values["SERVICE_DIR"] != instance.ServiceDir {
		return nil, errors.New("managed endpoint action environment does not match its approved instance")
	}
	if _, ok := values["NVIDIA_API_KEY"]; ok && instance.Registration.CredentialName != "NVIDIA_API_KEY" {
		return nil, errors.New("managed endpoint action has an unapproved credential name")
	}
	environment := managedEndpointBaseEnvironment()
	allowed := map[string]bool{"HOST_PORT": true, "SERVICE_DIR": true, "NVIDIA_API_KEY": true}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if !allowed[key] || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("managed endpoint action environment contains an unapproved field")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	environment = append(environment,
		"X_SCIENCE_ENDPOINT_INSTANCE="+instance.ID,
		"X_SCIENCE_ENDPOINT_USER="+instance.UserID,
		"X_SCIENCE_ENDPOINT_APPROVAL="+instance.ApprovedHash,
	)
	return environment, nil
}

// output is a descriptor owned by the durable endpoint worker, not an
// exec-managed copy goroutine. Wait observes only the finite shell process.
// A successful daemon may continue writing to output after this function exits.
func runManagedEndpointOwnedAction(ctx context.Context, script string, environment []string, output *os.File) managedEndpointActionExit {
	if ctx == nil || output == nil || strings.TrimSpace(script) == "" || strings.ContainsRune(script, '\x00') {
		return managedEndpointActionExit{Err: errors.New("managed endpoint action contract is invalid")}
	}
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	command.Env = append([]string(nil), environment...)
	command.Stdout, command.Stderr = output, output
	control, err := subprocess.Prepare(command)
	if err != nil {
		return managedEndpointActionExit{Err: fmt.Errorf("prepare managed endpoint action: %w", err)}
	}
	defer control.Close()
	if err := command.Start(); err != nil {
		return managedEndpointActionExit{Err: fmt.Errorf("start managed endpoint action: %w", err)}
	}
	if err := control.Attach(command); err != nil {
		killErr := control.Kill(command)
		waitErr := command.Wait()
		return managedEndpointActionExit{Err: errors.Join(fmt.Errorf("attach managed endpoint action: %w", err), killErr, waitErr)}
	}
	err = command.Wait()
	result := managedEndpointActionExit{Err: err}
	if command.ProcessState != nil {
		result.Exited = command.ProcessState.Exited() || err != nil
		code := command.ProcessState.ExitCode()
		result.Code = &code
	}
	if ctx.Err() != nil {
		result.Err = errors.Join(context.Cause(ctx), err)
	}
	return result
}
