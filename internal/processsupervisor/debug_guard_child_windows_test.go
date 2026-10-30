//go:build windows

package processsupervisor

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type guardChildReport struct {
	Debugged bool     `json:"debugged"`
	Input    string   `json:"input"`
	Value    string   `json:"value"`
	Dir      string   `json:"dir"`
	Args     []string `json:"args"`
}

func guardChildCommand(mode, trace string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestDebugGuardChild$", "--", "a b\"c", "中文\\tail")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "X_SCIENCE_GUARD_CHILD_MODE=") && !strings.HasPrefix(value, "X_SCIENCE_GUARD_VALUE=") && !strings.HasPrefix(value, "X_SCIENCE_GUARD_TRACE=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "X_SCIENCE_GUARD_CHILD_MODE="+mode, "X_SCIENCE_GUARD_VALUE=中文 α", "X_SCIENCE_GUARD_TRACE="+trace)
	return command
}

func TestDebugGuardChild(t *testing.T) {
	mode := os.Getenv("X_SCIENCE_GUARD_CHILD_MODE")
	if mode == "" {
		return
	}
	// First role-level user-code check after the supervisor's explicit Resume.
	debugged, _, _ := startupKernel.NewProc("IsDebuggerPresent").Call()
	if sentinel := os.Getenv("X_SCIENCE_GUARD_SENTINEL"); sentinel != "" {
		if err := os.WriteFile(sentinel, []byte("ran"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "hold" {
		for {
			time.Sleep(time.Hour) // A synthetic child waiting to be cancelled.
		}
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var arguments []string
	for index, value := range os.Args {
		if value == "--" {
			arguments = os.Args[index+1:]
			break
		}
	}
	report := guardChildReport{debugged != 0, string(input), os.Getenv("X_SCIENCE_GUARD_VALUE"), dir, arguments}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(os.Stderr, "stderr:中文 α")
	if value := os.Getenv("X_SCIENCE_GUARD_EXTRA_HANDLE"); value != "" {
		handle, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		file := os.NewFile(uintptr(handle), "owned-extra-handle")
		if _, err := io.WriteString(file, "extra-handle:中文 α"); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	if mode == "context-cancel" || mode == "wait-delay" {
		descendant := guardChildCommand("hold", "")
		descendant.Stdout, descendant.Stderr = os.Stdout, os.Stderr
		if err := descendant.Start(); err != nil {
			t.Fatal(err)
		}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(descendant.Process.Pid))
		if err != nil {
			_ = descendant.Process.Kill()
			t.Fatal(err)
		}
		created, err := processCreationTime(handle)
		windows.CloseHandle(handle)
		if err != nil {
			_ = descendant.Process.Kill()
			t.Fatal(err)
		}
		identity, _ := json.Marshal(ownedProcessIdentity{PID: uint32(descendant.Process.Pid), Created: created})
		if err := os.WriteFile(os.Getenv("X_SCIENCE_GUARD_TRACE")+".descendant", identity, 0600); err != nil {
			_ = descendant.Process.Kill()
			t.Fatal(err)
		}
		if mode == "context-cancel" {
			_ = descendant.Wait()
		} else {
			_ = descendant.Process.Release()
		}
	}
	if mode == "explicit-family" {
		descendant := guardChildCommand("report", "")
		descendant.Stdout, descendant.Stderr = os.Stdout, os.Stderr
		if err := descendant.Run(); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(0) // Do not append the Go test harness's PASS banner to child output.
}

func waitGuardDescendant(t *testing.T, path string) ownedProcessIdentity {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var identity ownedProcessIdentity
			if err := json.Unmarshal(data, &identity); err == nil && identity.PID != 0 && identity.Created != 0 {
				return identity
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture: no owned descendant creation identity")
	return ownedProcessIdentity{}
}
