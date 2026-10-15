//go:build linux

package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/compute"
)

func TestManagedEndpointNativePhysicalLifecycle(t *testing.T) {
	binary, systemctl := managedEndpointPhysicalPrerequisites(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native physical witness not run: existing python3 is unavailable")
	}
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("native physical witness not run: setsid is unavailable")
	}
	start := "set -eu\nprintf 'start\\n' >> \"$SERVICE_DIR/start-invocations\"\n" +
		managedEndpointPhysicalQuote(setsid) + " " + managedEndpointPhysicalQuote(python) +
		" -u \"$SERVICE_DIR/native-http.py\" main \"$X_SCIENCE_ENDPOINT_INSTANCE\" \"$HOST_PORT\" \"$SERVICE_DIR\" 127.0.0.1 &\n"
	stop := "set -eu\nprintf 'stop\\n' >> \"$SERVICE_DIR/stop-invocations\"\n"
	registration := managedEndpointPhysicalRegistration("native-unit", managedEndpointPhysicalPort(t), start, stop)
	registration.Owner.StartupTimeoutSeconds = 300
	fixture := newManagedEndpointPhysicalFixture(t, binary, systemctl, registration)
	if err := os.WriteFile(filepath.Join(fixture.instance.ServiceDir, "native-http.py"), []byte(managedEndpointPhysicalPython), 0o600); err != nil {
		t.Fatal(err)
	}
	coordinator, _ := fixture.coordinator(t)
	observerCtx, closeObserver := context.WithCancel(fixture.ctx)
	state, receipt, err := coordinator.Run(observerCtx, managedOwnerRecoveryClaim(fixture.managedEndpointOwnerFixture))
	closeObserver()
	if err != nil || state != "live" || receipt.ValidateFor(fixture.instance, fixture.action.Ref) != nil || !receipt.ProcessExited || receipt.ExitCode == nil || *receipt.ExitCode != 0 || receipt.OutputClosed {
		t.Fatalf("native shell exit/owner/EOF facts conflated: state=%s receipt=%#v err=%v", state, receipt, err)
	}
	fixture.reload(t, receipt.Ref)
	if fixture.instance.Registration.Owner == nil || fixture.instance.Registration.Owner.StartupTimeoutSeconds != 300 {
		t.Fatal("native worker did not preserve its approved startup budget")
	}
	if err := fixture.verifyPrivateWorker(fixture.ctx, fixture.instance.Identity); err != nil {
		t.Fatal(err)
	}
	var parent, child managedEndpointPhysicalProcess
	if err := managedEndpointPhysicalWait(fixture.ctx, func() error {
		return errors.Join(managedEndpointPhysicalReadJSON(filepath.Join(fixture.instance.ServiceDir, "parent.json"), &parent), managedEndpointPhysicalReadJSON(filepath.Join(fixture.instance.ServiceDir, "child.json"), &child))
	}); err != nil {
		t.Fatal(err)
	}
	if parent.PID == child.PID {
		t.Fatal("native fixture did not create a separate setsid child")
	}
	if err := managedEndpointPhysicalWait(fixture.ctx, func() error {
		return managedEndpointPhysicalLateOutput(fixture.instance)
	}); err != nil {
		t.Fatalf("daemon late output was lost/SIGPIPE after finite shell exit: %v", err)
	}
	fixture.health(t, 1)
	managedEndpointPhysicalScriptCount(t, fixture.instance.ServiceDir, 1)
	// A closed observer and reopened real Store do not own this systemd unit.
	// The same completed ref remains evidence, not a replay permission.
	fixture.reopen(t)
	reopened, _ := fixture.coordinator(t)
	if err := reopened.CheckLive(fixture.ctx, fixture.instance.UserID, fixture.instance.Endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Reconcile(fixture.ctx, nil, 4); err != nil {
		t.Fatal(err)
	}
	fixture.health(t, 1)
	managedEndpointPhysicalScriptCount(t, fixture.instance.ServiceDir, 1)
	for _, process := range []managedEndpointPhysicalProcess{parent, child} {
		if live, err := managedEndpointPhysicalProcessLive(process); err != nil || !live {
			t.Fatalf("observer/Store close killed a real native descendant: %#v err=%v", process, err)
		}
	}
	proof := fixture.stop(t)
	if proof.Identity != fixture.instance.Identity || proof.StoppedProof == nil || !proof.StoppedProof.CgroupEmpty {
		t.Fatal("native Stop did not retain its exact original owner proof")
	}
	if err := managedEndpointPhysicalWait(fixture.ctx, func() error {
		if err := managedEndpointPhysicalCgroupEmpty(proof.Identity.ControlGroup); err != nil {
			return err
		}
		for _, process := range []managedEndpointPhysicalProcess{parent, child} {
			live, err := managedEndpointPhysicalProcessLive(process)
			if err != nil || live {
				return errors.Join(err, errors.New("original native/setsid child remained live after exact Stop"))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual native owner invocation=%s cgroup=%s; shell exit + late output + HTTP, Store reopen, exact child cleanup proven; not 24h endurance", proof.Identity.InvocationID, proof.Identity.ControlGroup)
}

func managedEndpointPhysicalLateOutput(instance compute.ManagedEndpointOwnerInstance) error {
	directory := filepath.Join(instance.RuntimeRoot, instance.ID, "output")
	var retained strings.Builder
	var total int64
	for _, name := range []string{"output-0", "output-1"} {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > instance.Registration.Owner.Output.SegmentBytes {
			return errors.New("native physical output is foreign, missing or beyond its segment bound")
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += int64(len(value))
		retained.Write(value)
	}
	if total > instance.Registration.Owner.Output.SegmentBytes*int64(instance.Registration.Owner.Output.Segments) {
		return errors.New("native physical output exceeded its retained byte policy")
	}
	if !strings.Contains(retained.String(), "late-owned-output:"+instance.ID) {
		return errors.New("the original daemon has not written its late owned output")
	}
	return nil
}
