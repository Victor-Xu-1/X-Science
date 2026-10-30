//go:build windows

package processsupervisor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const ownerDeathRole = "X_SCIENCE_OWNER_DEATH_HELPER"

type ownedProcessIdentity struct {
	PID         uint32 `json:"pid"`
	Created     uint64 `json:"created"`
	Unavailable string `json:"unavailable,omitempty"`
}

// This is an actual process-death boundary, not an injected Start error: the
// launcher exits while its child is suspended and before Job assignment.
func TestOwnerDeathBeforeAssignmentDoesNotLeaveSuspendedChild(t *testing.T) {
	assertOwnerDeathContainment(t, "launcher")
}

func assertOwnerDeathContainment(t *testing.T, launcherRole string) {
	t.Helper()
	peer := launchOwnerDeathHelper(t, "peer", "")
	peerIdentity := peer.identity(t)
	peerHandle := retainOwnedProcess(t, peerIdentity)
	t.Cleanup(func() { reapOwnedProcess(t, peerHandle, "unrelated peer") })

	sentinel := filepath.Join(t.TempDir(), "child-ran")
	launcher := launchOwnerDeathHelper(t, launcherRole, sentinel)
	childIdentity := launcher.identity(t)
	childHandle := retainOwnedProcess(t, childIdentity)
	t.Cleanup(func() { reapOwnedProcess(t, childHandle, "suspended child") })
	// Only now may the launcher abandon its child: the test owns an exact handle
	// with a verified creation identity, so even a failing assertion is safe.
	control := "exit\n"
	if strings.HasPrefix(launcherRole, "guard-") {
		control = "continue\n"
	}
	if _, err := io.WriteString(launcher.input, control); err != nil {
		t.Fatalf("fixture: request launcher exit: %v", err)
	}
	select {
	case err := <-launcher.done:
		launcher.waited = true
		if err != nil {
			t.Fatalf("fixture: launcher exit: %v; stderr=%s", err, launcher.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixture: launcher did not exit")
	}
	status, err := windows.WaitForSingleObject(childHandle, 250)
	if err != nil {
		t.Fatalf("fixture: observe retained child: %v", err)
	}
	if status != windows.WAIT_OBJECT_0 {
		t.Errorf("OWNER_DEATH_CONTAINMENT_GAP: launcher exited but its exact suspended child survives: pid=%d creation=%d wait=%d", childIdentity.PID, childIdentity.Created, status)
	} else {
		t.Log("kernel killed the exact suspended child after launcher exit")
	}
	if status, err := windows.WaitForSingleObject(peerHandle, 0); err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Errorf("unrelated peer changed: wait=%d error=%v", status, err)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("suspended child ran before assignment: stat=%v", err)
	}
}

func TestDebugGuardOwnerDeathBoundaries(t *testing.T) {
	for _, boundary := range []string{"before-assign", "after-assign", "after-detach"} {
		t.Run(boundary, func(t *testing.T) {
			assertOwnerDeathContainment(t, boundary)
		})
	}
}

type ownerDeathHelper struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	done    chan error
	waited  bool
	stderr  bytes.Buffer
}

func launchOwnerDeathHelper(t *testing.T, role, sentinel string) *ownerDeathHelper {
	t.Helper()
	helper := &ownerDeathHelper{command: ownerDeathCommand(role, sentinel), done: make(chan error, 1)}
	var err error
	helper.input, err = helper.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	helper.output, err = helper.command.StdoutPipe()
	if err != nil {
		helper.input.Close()
		t.Fatal(err)
	}
	helper.command.Stderr = &helper.stderr
	if err := helper.command.Start(); err != nil {
		helper.input.Close()
		helper.output.Close()
		t.Fatalf("fixture: start %s: %v", role, err)
	}
	go func() { helper.done <- helper.command.Wait() }()
	t.Cleanup(func() {
		// EOF makes a launcher still at the seam return an error through Start's
		// existing cleanup. Never ask it to exit before retaining the child.
		helper.input.Close()
		helper.output.Close()
		if helper.waited {
			return
		}
		select {
		case <-helper.done:
		case <-time.After(7 * time.Second):
			_ = helper.command.Process.Kill() // Only this directly-created helper.
			select {
			case <-helper.done:
			case <-time.After(5 * time.Second):
				t.Errorf("fixture cleanup: directly-owned %s did not exit", role)
			}
		}
	})
	return helper
}

func (helper *ownerDeathHelper) identity(t *testing.T) ownedProcessIdentity {
	t.Helper()
	type result struct {
		identity ownedProcessIdentity
		err      error
	}
	ready := make(chan result, 1)
	go func() {
		var identity ownedProcessIdentity
		err := json.NewDecoder(io.LimitReader(helper.output, 1024)).Decode(&identity)
		ready <- result{identity, err}
	}()
	select {
	case observed := <-ready:
		if observed.identity.Unavailable != "" {
			t.Skipf("native capability baseline unavailable: %s", observed.identity.Unavailable)
		}
		if observed.err != nil || observed.identity.PID == 0 || observed.identity.Created == 0 {
			select {
			case err := <-helper.done:
				helper.waited = true
				t.Logf("fixture helper exited before identity: %v stderr=%s", err, helper.stderr.String())
			case <-time.After(time.Second):
			}
			t.Fatalf("fixture: invalid owned identity: %+v error=%v", observed.identity, observed.err)
		}
		return observed.identity
	case <-time.After(5 * time.Second):
		t.Fatal("fixture: no owned process identity")
		return ownedProcessIdentity{}
	}
}

func retainOwnedProcess(t *testing.T, identity ownedProcessIdentity) windows.Handle {
	t.Helper()
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, identity.PID)
	if err != nil {
		t.Fatalf("fixture: retain directly-reported process: %v", err)
	}
	created, err := processCreationTime(handle)
	if err != nil || created != identity.Created {
		windows.CloseHandle(handle)
		t.Fatalf("fixture: process creation identity mismatch: got=%d expected=%d error=%v", created, identity.Created, err)
	}
	t.Logf("retained exact process pid=%d creation=%d", identity.PID, identity.Created)
	return handle
}

func reapOwnedProcess(t *testing.T, handle windows.Handle, label string) {
	t.Helper()
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	if err == nil && status == uint32(windows.WAIT_TIMEOUT) {
		err = windows.TerminateProcess(handle, 1)
	}
	if err != nil {
		t.Errorf("fixture cleanup: terminate exact %s handle: %v", label, err)
		return
	}
	status, err = windows.WaitForSingleObject(handle, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Errorf("fixture cleanup: exact %s not reaped: wait=%d error=%v", label, status, err)
		return
	}
	t.Logf("cleanup verified: exact %s exited", label)
}

func processCreationTime(handle windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user)
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), err
}

func ownerDeathCommand(role, sentinel string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestOwnerDeathHelper$")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, ownerDeathRole+"=") && !strings.HasPrefix(value, processSupervisorSentinel+"=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, ownerDeathRole+"="+role, processSupervisorSentinel+"="+sentinel)
	return command
}

func TestOwnerDeathHelper(t *testing.T) {
	if role := os.Getenv(ownerDeathRole); strings.HasPrefix(role, "contract-") {
		runManagedStartContract(t, strings.TrimPrefix(role, "contract-"))
		return
	}
	switch os.Getenv(ownerDeathRole) {
	case "":
		return
	case "peer":
		created, err := processCreationTime(windows.CurrentProcess())
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(ownedProcessIdentity{PID: uint32(os.Getpid()), Created: created}); err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "child":
		if err := os.WriteFile(os.Getenv(processSupervisorSentinel), []byte("ran"), 0600); err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "launcher", "before-assign", "after-assign", "after-detach":
		child := ownerDeathCommand("child", os.Getenv(processSupervisorSentinel))
		api := systemWindowsProcessAPI
		api.openProcess = func(access uint32, inherit bool, pid uint32) (windows.Handle, error) {
			handle, err := windows.OpenProcess(access|windows.PROCESS_QUERY_LIMITED_INFORMATION, inherit, pid)
			if err != nil {
				return 0, err
			}
			created, err := processCreationTime(handle)
			if err == nil {
				err = json.NewEncoder(os.Stdout).Encode(ownedProcessIdentity{PID: pid, Created: created})
			}
			if err == nil {
				line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
				if readErr == nil && line == "exit\n" {
					if role := os.Getenv(ownerDeathRole); role == "launcher" || role == "before-assign" {
						os.Exit(0)
					}
					return handle, nil
				}
				err = fmt.Errorf("fixture abandoned handshake: %q: %w", line, readErr)
			}
			windows.CloseHandle(handle)
			return 0, err // Failed handshake goes through production cleanup.
		}
		api.assignProcessToJobObject = func(job, process windows.Handle) error {
			if err := windows.AssignProcessToJobObject(job, process); err != nil {
				return err
			}
			if os.Getenv(ownerDeathRole) == "after-assign" {
				os.Exit(0)
			}
			return nil
		}
		api.detachDebuggee = func(pid uint32) error {
			if err := detachStartupDebugger(pid); err != nil {
				return err
			}
			if os.Getenv(ownerDeathRole) == "after-detach" {
				os.Exit(0)
			}
			return nil
		}
		if job, err := start(child, api); err != nil {
			t.Fatal(err)
		} else {
			job.Terminate()
			job.Close()
			child.Wait()
			t.Fatal("fixture: launcher escaped the pre-assignment seam")
		}
	default:
		t.Fatal("unknown helper role")
	}
}
