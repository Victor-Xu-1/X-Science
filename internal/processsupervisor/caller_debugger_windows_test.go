//go:build windows

package processsupervisor

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This is a test-owned explicit debugger, not a production startup dispatcher.
// It deliberately consumes its own family's events to prove caller semantics.
type callerDebugEvent struct {
	Code, PID, TID, Padding uint32
	Data                    [160]byte
}

func runCallerDebuggerContract(t *testing.T, scenario string) {
	flags := uint32(1)
	if scenario == "caller-debug-only" {
		flags = 2
	}
	command := guardChildCommand("explicit-family", "")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	api := systemWindowsProcessAPI
	var callerID, createID uint32
	api.runStartupThread = func(func()) error { return errors.New("product replaced caller-owned debugger thread") }
	api.detachDebuggee = func(uint32) error { return errors.New("product detached caller-owned debugger") }
	api.openProcess = func(access uint32, inherit bool, pid uint32) (windows.Handle, error) {
		createID = windows.GetCurrentThreadId()
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
				err = errors.New("caller-owned child handshake abandoned")
			}
		}
		if err != nil {
			windows.CloseHandle(handle)
			return 0, err
		}
		return handle, nil
	}
	var job *Job
	var bodyErr error
	var events, waits int
	before := startupSequence.Load()
	nativeErr := runWindowsStartupThread(func() {
		callerID = windows.GetCurrentThreadId()
		job, bodyErr = start(command, api)
		if bodyErr != nil {
			return
		}
		events, bodyErr = pumpCallerDebugger(uint32(command.Process.Pid))
		if bodyErr != nil {
			return
		}
		waits++
		bodyErr = command.Wait()
	})
	if job != nil {
		defer job.Close()
	}
	if nativeErr != nil || bodyErr != nil {
		t.Errorf("caller debugger: native=%v body=%v", nativeErr, bodyErr)
		if job != nil {
			_ = job.Terminate()
		}
		return
	}
	var reports []guardChildReport
	decoder := json.NewDecoder(&stdout)
	for decoder.More() {
		var report guardChildReport
		if err := decoder.Decode(&report); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
	}
	expectedEvents := 2
	if flags == 2 {
		expectedEvents = 1
	}
	valid := callerID == createID && command.SysProcAttr.CreationFlags == flags|windows.CREATE_SUSPENDED && startupSequence.Load() == before+1 && waits == 1 && events == expectedEvents && len(reports) == 2 && reports[0].Debugged && reports[1].Debugged == (flags == 1)
	if !valid {
		t.Errorf("caller debugger semantics changed: caller=%d create=%d flags=%#x events=%d reports=%+v waits=%d", callerID, createID, command.SysProcAttr.CreationFlags, events, reports, waits)
	}
	data, _ := json.Marshal(map[string]any{"scenario": scenario, "caller_thread": callerID, "creation_thread": createID, "flags": command.SysProcAttr.CreationFlags, "debugged_process_events": events, "root_debugged": reports[0].Debugged, "descendant_debugged": reports[1].Debugged, "wait_count": waits, "valid": valid, "kill_on_exit_false_tested": false})
	_ = os.WriteFile(os.Getenv(processSupervisorSentinel), data, 0600)
}

func pumpCallerDebugger(root uint32) (int, error) {
	deadline := time.Now().Add(5 * time.Second)
	processes := 0
	for time.Now().Before(deadline) {
		var event callerDebugEvent
		ok, _, err := startupKernel.NewProc("WaitForDebugEvent").Call(uintptr(unsafe.Pointer(&event)), 100)
		if ok == 0 {
			if errors.Is(err, windows.ERROR_SEM_TIMEOUT) {
				continue
			}
			return processes, fmt.Errorf("caller WaitForDebugEvent: %w", err)
		}
		if event.Code == 3 {
			processes++
		}
		if event.Code == 3 || event.Code == 6 {
			if file := windows.Handle(binary.LittleEndian.Uint64(event.Data[:8])); file != 0 {
				_ = windows.CloseHandle(file)
			}
		}
		status := uintptr(0x00010002)
		if event.Code == 1 {
			code := binary.LittleEndian.Uint32(event.Data[:4])
			if code != 0x80000003 && code != 0x4000001f {
				status = 0x80010001
			}
		}
		continued, _, err := startupKernel.NewProc("ContinueDebugEvent").Call(uintptr(event.PID), uintptr(event.TID), status)
		if continued == 0 {
			return processes, fmt.Errorf("caller ContinueDebugEvent: %w", err)
		}
		if event.Code == 5 && event.PID == root {
			return processes, nil
		}
	}
	return processes, errors.New("test-owned debugger did not observe root exit")
}
