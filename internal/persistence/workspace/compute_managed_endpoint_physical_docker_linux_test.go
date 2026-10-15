//go:build linux

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

func managedEndpointPhysicalDockerPrerequisites(t *testing.T) (string, string) {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker physical witness not run: docker CLI is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	daemon, err := managedEndpointPhysicalCommand(ctx, docker, "--context=default", "info", "--format", "{{.ID}}")
	if err != nil || daemon == "" {
		t.Skipf("Docker physical witness not run: explicit default daemon unavailable (%v)", err)
	}
	image, err := managedEndpointPhysicalCommand(ctx, docker, "--context=default", "image", "inspect", "--format", "{{.Id}} {{.Os}}/{{.Architecture}}", "--", managedEndpointPhysicalImage)
	if err != nil || image != managedEndpointPhysicalImage+" linux/amd64" {
		t.Skipf("Docker physical witness not run: exact local image ID unavailable (%q, %v); no pull attempted", image, err)
	}
	return docker, daemon
}

type managedEndpointPhysicalDockerFacts struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	Labels   map[string]string `json:"labels"`
	Running  bool              `json:"running"`
	Status   string            `json:"status"`
	ExitCode int               `json:"exitCode"`
	Error    string            `json:"error"`
	Ports    map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	} `json:"ports"`
	LogType  string `json:"logType"`
	LogSize  string `json:"logSize"`
	LogFiles string `json:"logFiles"`
	Memory   int64  `json:"memory"`
	Swap     int64  `json:"swap"`
	NanoCPU  int64  `json:"nanoCpu"`
	PIDs     int64  `json:"pids"`
}

func (fixture *managedEndpointPhysicalFixture) dockerFacts(ctx context.Context, id string) (managedEndpointPhysicalDockerFacts, error) {
	var facts managedEndpointPhysicalDockerFacts
	if !managedEndpointPhysicalContainerID(id) {
		return facts, errors.New("private Docker operation requires a full immutable container ID")
	}
	daemon, err := managedEndpointPhysicalCommand(ctx, fixture.docker, "--context=default", "info", "--format", "{{.ID}}")
	if err != nil || daemon != fixture.daemon {
		return facts, errors.New("private Docker daemon identity changed")
	}
	const projection = `{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Image}},"labels":{"ai.x-science.endpoint-instance":{{json (index .Config.Labels "ai.x-science.endpoint-instance")}},"ai.x-science.endpoint-user":{{json (index .Config.Labels "ai.x-science.endpoint-user")}},"ai.x-science.endpoint-approval":{{json (index .Config.Labels "ai.x-science.endpoint-approval")}}},"running":{{json .State.Running}},"status":{{json .State.Status}},"exitCode":{{json .State.ExitCode}},"error":{{json .State.Error}},"ports":{{json .NetworkSettings.Ports}},"logType":{{json .HostConfig.LogConfig.Type}},"logSize":{{json (index .HostConfig.LogConfig.Config "max-size")}},"logFiles":{{json (index .HostConfig.LogConfig.Config "max-file")}},"memory":{{json .HostConfig.Memory}},"swap":{{json .HostConfig.MemorySwap}},"nanoCpu":{{json .HostConfig.NanoCpus}},"pids":{{json .HostConfig.PidsLimit}}}`
	raw, err := managedEndpointPhysicalCommand(ctx, fixture.docker, "--context=default", "container", "inspect", "--format", projection, "--", id)
	if err != nil {
		return facts, err
	}
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return facts, err
	}
	spec := fixture.registration.Owner.Docker
	if facts.ID != id || facts.Name != "/"+spec.Container || facts.Image != managedEndpointPhysicalImage ||
		facts.Labels["ai.x-science.endpoint-instance"] != fixture.instance.ID || facts.Labels["ai.x-science.endpoint-user"] != fixture.instance.UserID || facts.Labels["ai.x-science.endpoint-approval"] != fixture.instance.ApprovedHash {
		return facts, errors.New("private Docker identity, image, locator or ownership labels changed")
	}
	if facts.LogType != "json-file" || facts.LogSize != "64k" || facts.LogFiles != "2" || facts.Memory != 96<<20 || facts.Swap != facts.Memory || facts.NanoCPU != 250000000 || facts.PIDs != 32 {
		return facts, errors.New("private Docker effective logging or resource bounds are not the approved finite values")
	}
	return facts, nil
}

func (fixture *managedEndpointPhysicalFixture) cleanupDocker(ctx context.Context) error {
	id := fixture.containerID
	if id == "" {
		// A lost create acknowledgement may precede the receipt. Enumerate only
		// this Store-allocated instance label + exact private locator; do not kill
		// by name. Every returned full ID still requires fresh typed inspection.
		spec := fixture.registration.Owner.Docker
		raw, err := managedEndpointPhysicalCommand(ctx, fixture.docker, "--context=default", "container", "ls", "--all", "--no-trunc", "--filter", "label=ai.x-science.endpoint-instance="+fixture.instance.ID, "--filter", "name=^/"+spec.Container+"$", "--format", "{{.ID}}")
		if err != nil {
			return err
		}
		ids := strings.Fields(raw)
		if len(ids) == 0 {
			return nil
		}
		if len(ids) != 1 {
			return errors.New("private Docker fixture enumeration was not unique")
		}
		id = ids[0]
	}
	facts, err := fixture.dockerFacts(ctx, id)
	if err != nil {
		return err
	}
	if facts.Running {
		if _, err := managedEndpointPhysicalCommand(ctx, fixture.docker, "--context=default", "container", "stop", "--time=1", "--", id); err != nil {
			return err
		}
		facts, err = fixture.dockerFacts(ctx, id)
		if err != nil {
			return err
		}
	}
	if facts.Running || facts.Status != "exited" && facts.Status != "dead" {
		return errors.New("private Docker container is not positively stopped; removal refused")
	}
	_, err = managedEndpointPhysicalCommand(ctx, fixture.docker, "--context=default", "container", "rm", "--", id)
	return err
}

func managedEndpointPhysicalContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, value := range id {
		if value < '0' || value > '9' {
			if value < 'a' || value > 'f' {
				return false
			}
		}
	}
	return true
}

func managedEndpointPhysicalDockerScripts(name string) (string, string) {
	readID := "cid=$(cat \"$SERVICE_DIR/container-id\")\ntest \"${#cid}\" -eq 64\ncase \"$cid\" in *[!0-9a-f]*) exit 92 ;; esac\n"
	start := "set -eu\nprintf 'start\\n' >> \"$SERVICE_DIR/start-invocations\"\n" +
		"if test -s \"$SERVICE_DIR/container-id\"; then\n" + readID + "docker --context=default container start -- \"$cid\"\nelse\n" +
		"docker --context=default run --pull=never --detach --name " + managedEndpointPhysicalQuote(name) +
		" --cidfile \"$SERVICE_DIR/container-id\" --memory=96m --memory-swap=96m --cpus=0.25 --pids-limit=32" +
		" --cap-drop=ALL --security-opt=no-new-privileges --log-driver=json-file --log-opt=max-size=64k --log-opt=max-file=2" +
		" --label \"ai.x-science.endpoint-instance=$X_SCIENCE_ENDPOINT_INSTANCE\" --label \"ai.x-science.endpoint-user=$X_SCIENCE_ENDPOINT_USER\" --label \"ai.x-science.endpoint-approval=$X_SCIENCE_ENDPOINT_APPROVAL\"" +
		" --publish \"127.0.0.1:$HOST_PORT:8080\" --entrypoint=python3 " + managedEndpointPhysicalQuote(managedEndpointPhysicalImage) +
		" -u -c " + managedEndpointPhysicalQuote(managedEndpointPhysicalPython) + " main \"$X_SCIENCE_ENDPOINT_INSTANCE\" 8080 /tmp 0.0.0.0\nfi\n"
	stop := "set -eu\n" + readID + "docker --context=default container stop --time=1 -- \"$cid\"\n"
	return start, stop
}

func TestManagedEndpointDockerPhysicalLifecycle(t *testing.T) {
	binary, systemctl := managedEndpointPhysicalPrerequisites(t)
	docker, daemon := managedEndpointPhysicalDockerPrerequisites(t)
	port := managedEndpointPhysicalPort(t)
	name := fmt.Sprintf("x-science-private-witness-%d-%d", os.Getpid(), port)
	start, stop := managedEndpointPhysicalDockerScripts(name)
	registration := managedEndpointPhysicalRegistration("docker", port, start, stop)
	registration.Owner.Docker = &compute.ManagedEndpointDockerSpec{Context: "default", Container: name}
	fixture := newManagedEndpointPhysicalFixture(t, binary, systemctl, registration)
	fixture.docker, fixture.daemon = docker, daemon
	_, runtime := fixture.coordinator(t)
	environment := map[string]string{"HOST_PORT": fmt.Sprint(port), "SERVICE_DIR": fixture.instance.ServiceDir}
	// Keep the accepted successful receipt local first. The DB still owns the
	// original pending ref when the output worker is deliberately lost.
	receipt, err := runtime.Start(fixture.ctx, fixture.instance, fixture.action, environment)
	if err != nil || receipt.ValidateFor(fixture.instance, fixture.action.Ref) != nil {
		fixture.diagnoseDockerStart(t)
		t.Fatalf("physical Docker Start receipt=%#v err=%v", receipt, err)
	}
	fixture.containerID = receipt.Identity.ContainerID
	accepted := fixture.reload(t, fixture.action.Ref)
	if !accepted.Accepted || accepted.Receipt != nil {
		t.Fatalf("worker-loss setup did not retain the original pending accepted action: %#v", accepted)
	}
	if _, err := fixture.dockerFacts(fixture.ctx, fixture.containerID); err != nil {
		t.Fatal(err)
	}
	fixture.health(t, 1)
	if err := fixture.stopPrivateWorker(fixture.ctx, fixture.instance.WorkerIdentity); err != nil {
		t.Fatal(err)
	}
	fixture.reopen(t)
	_, reopenedRuntime := fixture.coordinator(t)
	recovered, err := reopenedRuntime.Observe(fixture.ctx, fixture.instance, accepted)
	if err != nil || recovered.ValidateFor(fixture.instance, accepted.Ref) != nil || recovered.Identity != receipt.Identity || recovered.OutputWarning == "" {
		t.Fatalf("exact successful receipt was not recovered without replay: %#v err=%v", recovered, err)
	}
	if state, err := fixture.store.FinishManagedEndpointAction(fixture.ctx, accepted.Ref, recovered); err != nil || state != "live" {
		t.Fatalf("recovered physical owner state=%s err=%v", state, err)
	}
	fixture.reload(t, accepted.Ref)
	fixture.health(t, 1)
	managedEndpointPhysicalScriptCount(t, fixture.instance.ServiceDir, 1)
	originalID, originalOrigin := fixture.instance.ID, fixture.instance.InitialAction
	fixture.stop(t)
	claimed, run, err := fixture.store.ClaimManagedEndpointStart(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot)
	if err != nil || !run {
		t.Fatalf("physical warm Start claim=%t err=%v", run, err)
	}
	coordinator, _ := fixture.coordinator(t)
	state, warm, err := coordinator.Run(fixture.ctx, managedEndpointOwnerTestClaim(claimed, fixture.registration))
	if err != nil || state != "live" || warm.Identity != receipt.Identity || warm.Ref.Generation <= accepted.Ref.Generation || warm.Ref.ClaimHandle == accepted.Ref.ClaimHandle {
		t.Fatalf("warm Start changed physical owner or reused admission: state=%s receipt=%#v err=%v", state, warm, err)
	}
	warmAction := fixture.reload(t, warm.Ref)
	if fixture.instance.ID != originalID || fixture.instance.InitialAction != originalOrigin || !warmAction.Accepted {
		t.Fatal("warm Start rewrote instance origin or skipped fresh Accept")
	}
	fixture.health(t, 2)
	// Typed warm Start restarts the same container without replaying bootstrap.
	managedEndpointPhysicalScriptCount(t, fixture.instance.ServiceDir, 1)
	if _, err := fixture.dockerFacts(fixture.ctx, fixture.containerID); err != nil {
		t.Fatal(err)
	}
	fixture.stop(t)
	t.Logf("actual Docker daemon=%s container=%s; exact successful-receipt recovery, same-ID warm admission and Stop proven; no science/endurance claim", daemon, fixture.containerID)
}

func managedEndpointPhysicalScriptCount(t *testing.T, directory string, expected int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, "start-invocations"))
	if err != nil || len(raw) > 128 || len(strings.Fields(string(raw))) != expected {
		t.Fatalf("approved Start script replayed or missing: %q err=%v", raw, err)
	}
}
