//go:build windows

package processsupervisor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type guardCapability struct {
	read, write *os.File
	handle      windows.Handle
	token       windows.Token
}

func (capability *guardCapability) closeWrite() {
	if capability.write != nil {
		_ = capability.write.Close()
		capability.write = nil
	}
}

func (capability *guardCapability) close() {
	capability.closeWrite()
	if capability.read != nil {
		_ = capability.read.Close()
	}
	if capability.handle != 0 {
		_ = windows.CloseHandle(capability.handle)
	}
	if capability.token != 0 {
		_ = capability.token.Close()
	}
}

func configureGuardCapability(t *testing.T, command *exec.Cmd, scenario, trace string) (*guardCapability, bool) {
	capability := &guardCapability{}
	attributes := syscall.SysProcAttr{}
	if scenario == "parent-process" {
		data, err := os.ReadFile(trace + ".parent")
		if err != nil {
			t.Fatal(err)
		}
		var parent ownedProcessIdentity
		if err := json.Unmarshal(data, &parent); err != nil {
			t.Fatal(err)
		}
		handle, err := windows.OpenProcess(windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, parent.PID)
		if err != nil {
			return unavailableGuardCapability(capability, err)
		}
		capability.handle = handle
		created, err := processCreationTime(handle)
		if err != nil || created != parent.Created {
			t.Fatalf("owned ParentProcess identity mismatch: %v", err)
		}
		attributes.ParentProcess = syscall.Handle(handle)
	}
	if scenario == "restricted-token" {
		var source windows.Token
		if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &source); err != nil {
			return unavailableGuardCapability(capability, err)
		}
		var restricted windows.Token
		result, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken").Call(uintptr(source), 1, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
		_ = source.Close()
		if result == 0 {
			return unavailableGuardCapability(capability, fmt.Errorf("own restricted token: %w", err))
		}
		capability.token = restricted
		attributes.Token = syscall.Token(restricted)
	}
	if scenario == "extra-handle" {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		capability.read, capability.write = read, write
		if err := windows.SetHandleInformation(windows.Handle(write.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			t.Fatal(err)
		}
		attributes.AdditionalInheritedHandles = []syscall.Handle{syscall.Handle(write.Fd())}
		command.Env = append(command.Env, "X_SCIENCE_GUARD_EXTRA_HANDLE="+strconv.FormatUint(uint64(write.Fd()), 10))
	}
	if scenario == "parent-process" || scenario == "restricted-token" {
		// Only claim a capability regression if this exact safe account/owned
		// parent could first run with the original public Cmd implementation.
		baseline := guardChildCommand("report", "")
		baseline.SysProcAttr = &attributes
		baseline.Dir = command.Dir
		var output bytes.Buffer
		baseline.Stdout, baseline.Stderr = &output, &output
		if err := baseline.Run(); err != nil {
			return unavailableGuardCapability(capability, fmt.Errorf("original Cmd baseline: %w", err))
		}
	}
	command.SysProcAttr = &attributes
	return capability, true
}

func unavailableGuardCapability(capability *guardCapability, err error) (*guardCapability, bool) {
	capability.close()
	_ = json.NewEncoder(os.Stdout).Encode(ownedProcessIdentity{Unavailable: err.Error()})
	return &guardCapability{}, false
}
