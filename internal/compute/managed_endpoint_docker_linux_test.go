//go:build linux

package compute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedEndpointDockerRejectsForeignSameNameAndUnboundedLogs(t *testing.T) {
	instance := ManagedEndpointOwnerInstance{ID: "private-inspect", UserID: "owner-a", ApprovedHash: strings.Repeat("1", 64), Registration: ManagedEndpointRegistration{Owner: &ManagedEndpointOwnerSpec{Kind: "docker", Docker: &ManagedEndpointDockerSpec{Context: "default", Container: "private-container"}}}, Identity: ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: "fixture-daemon", ContainerID: strings.Repeat("a", 64)}}
	path := filepath.Join(t.TempDir(), "docker-fixture")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$2 $3\" in\n 'info --format') printf 'fixture-daemon\\n';;\n 'container ls') printf '%s\\n';;\n 'container inspect') printf '%%s\\n' '{\"Id\":\"%s\",\"State\":{\"Status\":\"exited\",\"Running\":false},\"HostConfig\":{\"LogConfig\":{\"Type\":\"json-file\",\"Config\":{}}}}';;\n *) exit 90;;\nesac\n", strings.Repeat("b", 64), instance.Identity.ContainerID)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := &managedEndpointDocker{executable: path}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.validateNamedOwner(ctx, instance, false); err == nil || !strings.Contains(err.Error(), "rebound") {
		t.Fatalf("foreign same-name container was accepted: %v", err)
	}
	_, state, err := runtime.inspect(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if validateManagedEndpointDockerLogging(state) == nil {
		t.Fatal("default json-file driver was mistaken for bounded log retention")
	}
	state.HostConfig.LogConfig.Config = map[string]string{"max-size": "4m", "max-file": "2"}
	if err := validateManagedEndpointDockerLogging(state); err != nil {
		t.Fatal(err)
	}
}
