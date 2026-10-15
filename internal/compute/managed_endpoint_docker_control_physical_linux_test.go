//go:build linux

package compute

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Opt-in control-only evidence: no image pull, published port, model or science.
// The "foreign" replacement is another resource-owned fixture, never a user
// container. This does not assert HTTP readiness or full endpoint acceptance.
func TestManagedEndpointDockerPhysicalControlKeepsSameNameReplacementRunning(t *testing.T) {
	image := os.Getenv("X_SCIENCE_TEST_DOCKER_OWNER_CONTROL_IMAGE")
	if image == "" {
		t.Skip("explicit immutable local image required for physical Docker control")
	}
	if len(image) != 71 || !strings.HasPrefix(image, "sha256:") {
		t.Fatal("physical control image must be a full immutable sha256 ID")
	}
	for _, value := range image[7:] {
		if !strings.ContainsRune("0123456789abcdef", value) {
			t.Fatal("physical control image ID is malformed")
		}
	}
	runtime, err := newManagedEndpointDocker()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	scope := "x-science-control-" + uuid.NewString()
	spec := ManagedEndpointDockerSpec{Context: "default", Container: scope}
	daemon, err := runtime.command(ctx, spec, "info", "--format", "{{.ID}}")
	if err != nil || strings.TrimSpace(daemon) == "" {
		t.Fatalf("actual daemon observation failed: %v", err)
	}
	daemon = strings.TrimSpace(daemon)
	observedImage, err := runtime.command(ctx, spec, "image", "inspect", "--format", "{{.Id}}", "--", image)
	if err != nil || strings.TrimSpace(observedImage) != image {
		t.Fatalf("exact local image is unavailable; no pull permitted: %v", err)
	}
	created := []string{}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		for index := len(created) - 1; index >= 0; index-- {
			id := created[index]
			currentDaemon, daemonErr := runtime.command(cleanup, spec, "info", "--format", "{{.ID}}")
			facts, inspectErr := runtime.command(cleanup, spec, "container", "inspect", "--format", "{{.Id}} {{index .Config.Labels \"ai.x-science.control-test\"}}", "--", id)
			if daemonErr != nil || inspectErr != nil || strings.TrimSpace(currentDaemon) != daemon || strings.TrimSpace(facts) != id+" "+scope {
				t.Errorf("preserved unknown fixture cleanup target id=%s daemonErr=%v inspectErr=%v", id, daemonErr, inspectErr)
				continue
			}
			if _, err := runtime.command(cleanup, spec, "container", "rm", "--force", "--", id); err != nil {
				t.Errorf("exact owned fixture cleanup failed id=%s: %v", id, err)
			}
		}
	})
	create := func(name string) string {
		t.Helper()
		value, err := runtime.command(ctx, spec, "container", "create", "--pull=never", "--name="+name,
			"--network=none", "--read-only", "--memory=32m", "--memory-swap=32m", "--cpus=0.1", "--pids-limit=16",
			"--cap-drop=ALL", "--security-opt=no-new-privileges", "--log-driver=local", "--log-opt=max-size=4k", "--log-opt=max-file=2",
			"--label=ai.x-science.control-test="+scope, "--entrypoint=python3", image, "-I", "-c", "import time; time.sleep(60)")
		if err != nil {
			t.Fatalf("private bounded container creation failed: %v", err)
		}
		id := strings.TrimSpace(value)
		if validateManagedEndpointPhysicalIdentity(ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: daemon, ContainerID: id}) != nil {
			t.Fatal("actual created container did not return an immutable ID")
		}
		created = append(created, id)
		if _, err := runtime.command(ctx, spec, "container", "start", "--", id); err != nil {
			t.Fatalf("private container startup failed id=%s: %v", id, err)
		}
		return id
	}
	original := create(spec.Container)
	if _, err := runtime.command(ctx, spec, "container", "rename", original, spec.Container+"-parked"); err != nil {
		t.Fatal(err)
	}
	replacement := create(spec.Container)
	replacementStart, err := runtime.command(ctx, spec, "container", "inspect", "--format", "{{.State.StartedAt}}", "--", replacement)
	if err != nil {
		t.Fatal(err)
	}
	registration := ManagedEndpointRegistration{Name: scope, URL: "http://127.0.0.1:25001", Port: 25001, LivePath: "/health",
		StartScript: "bootstrap must not be replayed", StopScript: "name-based script must not execute",
		Owner: &ManagedEndpointOwnerSpec{Version: 1, Kind: "docker", Docker: &spec, Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	root := t.TempDir()
	instance := ManagedEndpointOwnerInstance{ID: scope, UserID: "controlled-physical-fixture", Endpoint: scope,
		Registration: registration, ApprovedHash: ApprovedManagedEndpointHash(registration), StartGeneration: 1,
		Identity:    ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: daemon, ContainerID: original},
		RuntimeRoot: root, ServiceDir: root, WorkspacePath: filepath.Join(root, "unused-workspace.sqlite"),
		InitialAction: ManagedEndpointActionRef{InstanceID: scope, Kind: "start", Generation: 1, ClaimHandle: "controlled"}}
	output, err := os.OpenFile(filepath.Join(root, "control-output"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	for _, kind := range []string{"stop", "start"} {
		exit := runtime.controlOwned(ctx, instance, kind, output)
		if exit.Err != nil || !exit.Exited || exit.Code == nil || *exit.Code != 0 {
			t.Fatalf("actual immutable-ID %s control failed: %+v", kind, exit)
		}
		_, current, err := runtime.inspect(ctx, instance)
		if err != nil || current.State.Running != (kind == "start") {
			t.Fatalf("original immutable owner did not reach %s: %+v %v", kind, current.State, err)
		}
		actual, err := runtime.command(ctx, spec, "container", "inspect", "--format", "{{.State.Running}} {{.State.StartedAt}}", "--", replacement)
		if err != nil || strings.TrimSpace(actual) != "true "+strings.TrimSpace(replacementStart) {
			t.Fatalf("same-name replacement was interrupted by %s: %q %v", kind, actual, err)
		}
	}
	t.Logf("actual daemon=%s immutable owner=%s same-name replacement=%s remained running across typed stop/start", daemon, original, replacement)
}
