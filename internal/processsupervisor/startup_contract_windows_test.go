//go:build windows

package processsupervisor

import (
	"bufio"
	"bytes"
	"context"
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
	"unsafe"

	"golang.org/x/sys/windows"
)

type managedStartObservation struct {
	Scenario                                                string `json:"scenario"`
	Assigned, Detached, NativeThreadExited, RegistryCleared bool
	FailedStartReaped, IO, DescendantExited, ExtraHandle    bool
	WaitFailureInjected, ReportedThreadExited               bool
	SuspendCount                                            uint32
	WaitCount                                               int
	StartError, WaitError                                   string
}

func TestManagedWindowsStartContract(t *testing.T) {
	for _, scenario := range []string{"stdio", "context-cancel", "wait-delay", "open-failure", "assign-failure", "detach-failure", "resume-failure", "callback-panic", "cancel-before-handoff", "parent-process", "restricted-token", "extra-handle", "caller-debug-all", "caller-debug-only", "thread-wait-failure"} {
		t.Run(scenario, func(t *testing.T) {
			peer := launchOwnerDeathHelper(t, "peer", "")
			identity := peer.identity(t)
			peerHandle := retainOwnedProcess(t, identity)
			t.Cleanup(func() { reapOwnedProcess(t, peerHandle, "unrelated peer") })
			dir := filepath.Join(t.TempDir(), "工作 space")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(dir, "observation.json")
			data, _ := json.Marshal(identity)
			if err := os.WriteFile(trace+".parent", data, 0600); err != nil {
				t.Fatal(err)
			}
			helper := launchOwnerDeathHelper(t, "contract-"+scenario, trace)
			child := retainOwnedProcess(t, helper.identity(t))
			t.Cleanup(func() { reapOwnedProcess(t, child, "managed child") })
			if _, err := io.WriteString(helper.input, "continue\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-helper.done:
				helper.waited = true
				if err != nil {
					t.Errorf("managed contract helper: %v stderr=%s", err, helper.stderr.String())
				}
			case <-time.After(10 * time.Second):
				t.Error("managed contract helper exceeded deadline")
				_ = helper.command.Process.Kill()
				<-helper.done
				helper.waited = true
			}
			if data, err := os.ReadFile(trace); err != nil {
				t.Error(err)
			} else {
				t.Logf("managed observation: %s", data)
			}
			if state, err := windows.WaitForSingleObject(child, 1000); err != nil || state != windows.WAIT_OBJECT_0 {
				t.Errorf("child not terminated: wait=%d error=%v", state, err)
			}
			if state, err := windows.WaitForSingleObject(peerHandle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
				t.Errorf("peer changed: wait=%d error=%v", state, err)
			}
		})
	}
}

func runManagedStartContract(t *testing.T, scenario string) {
	if strings.HasPrefix(scenario, "caller-debug-") {
		runCallerDebuggerContract(t, scenario)
		return
	}
	trace := os.Getenv(processSupervisorSentinel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := guardChildCommand("report", trace)
	if scenario == "context-cancel" || scenario == "cancel-before-handoff" {
		original := command
		command = exec.CommandContext(ctx, original.Path, original.Args[1:]...)
		command.Env = original.Env
	}
	command.Dir = filepath.Dir(trace)
	command.Stdin = strings.NewReader("stdin \"quoted\" 中文 α\n")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = 150 * time.Millisecond
	if scenario == "context-cancel" || scenario == "wait-delay" {
		command.Env = append(command.Env, "X_SCIENCE_GUARD_CHILD_MODE="+scenario)
	}
	capability, available := configureGuardCapability(t, command, scenario, trace)
	if !available {
		return
	}
	defer capability.close()
	observation := managedStartObservation{Scenario: scenario}
	defer func() { data, _ := json.Marshal(observation); _ = os.WriteFile(trace, data, 0600) }()
	api := systemWindowsProcessAPI
	var process, ownJob windows.Handle
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
			if readErr != nil || line != "continue\n" {
				err = errors.New("owned child handshake abandoned")
			}
		}
		if err != nil || scenario == "open-failure" {
			windows.CloseHandle(handle)
			return 0, errors.Join(err, errors.New("injected open failure"))
		}
		process = handle
		return handle, nil
	}
	api.assignProcessToJobObject = func(job, child windows.Handle) error {
		if scenario == "assign-failure" {
			return errors.New("injected assign failure")
		}
		if err := windows.AssignProcessToJobObject(job, child); err != nil {
			return err
		}
		ownJob = job
		observation.Assigned = true
		if scenario == "cancel-before-handoff" {
			cancel()
		}
		return nil
	}
	api.detachDebuggee = func(pid uint32) error {
		if err := managedChildIsInJob(process, ownJob); err != nil {
			return err
		}
		if scenario == "detach-failure" {
			return errors.New("injected detach failure")
		}
		if err := detachStartupDebugger(pid); err != nil {
			return err
		}
		observation.Detached = true
		return nil
	}
	api.resumeThread = func(thread windows.Handle) (uint32, error) {
		if scenario == "resume-failure" {
			return 0, errors.New("injected resume failure")
		}
		if scenario == "callback-panic" {
			panic("controlled callback panic")
		}
		count, err := windows.ResumeThread(thread)
		observation.SuspendCount = count
		return count, err
	}
	api.runStartupThread = func(work func()) error {
		threadAPI := systemStartupThreadAPI
		threadAPI.wait = func(thread windows.Handle, milliseconds uint32) (uint32, error) {
			state, err := windows.WaitForSingleObject(thread, milliseconds)
			observation.NativeThreadExited = err == nil && state == windows.WAIT_OBJECT_0
			if scenario == "thread-wait-failure" && observation.NativeThreadExited {
				// Run the real wait on the exact owned thread. Publication and
				// physical exit happened, but force only the API result to fail:
				// the wrapper must not infer retired=true from completion alone.
				observation.WaitFailureInjected = true
				return windows.WAIT_FAILED, windows.ERROR_INVALID_HANDLE
			}
			return state, err
		}
		err := runStartupThreadWithAPI(work, threadAPI)
		observation.ReportedThreadExited = startupThreadHasExited(err)
		if scenario == "thread-wait-failure" && (!observation.WaitFailureInjected || err == nil || observation.ReportedThreadExited) {
			t.Error("failed wait must remain an explicit unproven-retirement error")
		}
		count := 0
		startupRequests.Range(func(_, _ any) bool { count++; return true })
		observation.RegistryCleared = count == 0
		return err
	}
	job, err := start(command, api)
	if command.SysProcAttr.CreationFlags&0x00000003 != 0 {
		t.Error("temporary creation debugger flags leaked into caller configuration")
	}
	if !observation.NativeThreadExited || !observation.RegistryCleared {
		t.Error("native startup owner not retired before Start result")
	}
	failed := strings.HasSuffix(scenario, "failure") || scenario == "callback-panic"
	if err != nil {
		observation.StartError = err.Error()
		observation.FailedStartReaped = command.ProcessState != nil && command.ProcessState.Exited()
		if (!failed && scenario != "cancel-before-handoff") || !observation.FailedStartReaped {
			t.Errorf("failed start not fully reaped: %v", err)
		}
		return
	}
	if job == nil {
		t.Fatal("successful Start has no Job")
	}
	defer job.Close()
	if failed {
		t.Error("injected failed startup unexpectedly succeeded")
		_ = job.Terminate()
	}
	var descendant windows.Handle
	if scenario == "context-cancel" || scenario == "wait-delay" {
		descendant = retainOwnedProcess(t, waitGuardDescendant(t, trace+".descendant"))
		defer windows.CloseHandle(descendant)
		if scenario == "context-cancel" {
			cancel()
		}
	}
	capability.closeWrite()
	observation.WaitCount++
	err = command.Wait()
	if err != nil {
		observation.WaitError = err.Error()
	}
	if scenario == "stdio" || scenario == "parent-process" || scenario == "restricted-token" || scenario == "extra-handle" {
		var report guardChildReport
		decodeErr := json.Unmarshal(stdout.Bytes(), &report)
		observation.IO = err == nil && decodeErr == nil && !report.Debugged && report.Input == "stdin \"quoted\" 中文 α\n" && report.Value == "中文 α" && report.Dir == command.Dir && len(report.Args) == 2 && report.Args[0] == "a b\"c" && report.Args[1] == "中文\\tail" && stderr.String() == "stderr:中文 α"
		if !observation.IO {
			t.Errorf("Cmd contract mismatch: err=%v decode=%v report=%+v stderr=%q", err, decodeErr, report, stderr.String())
		}
	}
	if scenario == "context-cancel" && err == nil {
		t.Error("cancelled command succeeded")
	}
	if scenario == "cancel-before-handoff" {
		observation.FailedStartReaped = command.ProcessState != nil && command.ProcessState.Exited()
		if err == nil || !observation.FailedStartReaped {
			t.Error("cancellation must end with a non-successful, fully-reaped Cmd.Wait")
		}
	}
	if scenario == "wait-delay" && !errors.Is(err, exec.ErrWaitDelay) {
		t.Errorf("WaitDelay not preserved: %v", err)
	}
	if descendant != 0 {
		_ = job.Terminate()
		state, err := windows.WaitForSingleObject(descendant, 1000)
		observation.DescendantExited = err == nil && state == windows.WAIT_OBJECT_0
		if !observation.DescendantExited {
			t.Errorf("descendant survived: %v", err)
		}
	}
	if capability.read != nil {
		data, err := io.ReadAll(capability.read)
		observation.ExtraHandle = err == nil && string(data) == "extra-handle:中文 α"
		if !observation.ExtraHandle {
			t.Errorf("extra handle data=%q error=%v", data, err)
		}
	}
}

func managedChildIsInJob(process, job windows.Handle) error {
	var contained int32
	ok, _, err := startupKernel.NewProc("IsProcessInJob").Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&contained)))
	if ok == 0 || contained == 0 {
		return fmt.Errorf("exact Job membership missing: native=%d member=%d error=%v", ok, contained, err)
	}
	return nil
}
