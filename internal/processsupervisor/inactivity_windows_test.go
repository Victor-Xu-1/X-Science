//go:build windows

package processsupervisor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsSilentFileIOKeepsInstallerWatchdogAlive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.bin")
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsSilentFileIOHelper$")
	command.Env = append(os.Environ(), "X_SCIENCE_SILENT_IO_TEST_PATH="+path)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	started := time.Now()
	watchdog := NewInactivityWatchdog(180 * time.Millisecond)
	if err := watchdog.Wait(context.Background(), command.Process.Pid, done, command.Process.Kill); err != nil {
		t.Fatalf("silent file I/O did not extend the activity window: %v", err)
	}
	if time.Since(started) < 450*time.Millisecond {
		t.Fatal("helper exited before the inactivity window was exercised")
	}
}

func TestWindowsSilentFileIOHelper(t *testing.T) {
	path := os.Getenv("X_SCIENCE_SILENT_IO_TEST_PATH")
	if path == "" {
		t.Skip("launched only by the process-activity integration test")
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data := make([]byte, 4096)
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestWindowsProcessActivityTracksOnlyLiveProcess(t *testing.T) {
	const helper = "X_SCIENCE_INACTIVITY_EXIT_HELPER"
	// 259 is also the value of STILL_ACTIVE, so the exit code alone cannot
	// prove liveness even when the process object is still accessible.
	const exitCode = 259
	if os.Getenv(helper) == "1" {
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			os.Exit(1)
		}
		os.Exit(exitCode)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessActivityTracksOnlyLiveProcess$")
	command.Env = append(os.Environ(), helper+"=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	pid := command.Process.Pid
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	created, err := processCreationTime(handle)
	if err != nil || created == 0 {
		t.Fatalf("fixture birth time: %d, %v", created, err)
	}
	identity := processActivityIdentity{PID: pid, StartTicks: created}
	live := sampleProcessActivity(pid)
	if _, present := live.Processes[identity]; !live.Available || !present {
		t.Fatalf("live process activity lost exact birth identity: %+v", live)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("fixture exit wait: status=%d error=%v", status, err)
	}
	_ = command.Wait()
	var actualExit uint32
	if err := windows.GetExitCodeProcess(handle, &actualExit); err != nil || actualExit != exitCode {
		t.Fatalf("held process must have exited with %d: code=%d error=%v", exitCode, actualExit, err)
	}
	exited := sampleProcessActivity(pid)
	if exited.Available || len(exited.Processes) != 0 {
		t.Fatalf("signaled process with retained handle still sampled as live: %+v", exited)
	}
	if !processActivityAdvanced(live, exited) {
		t.Fatal("observed process exit must give command.Wait a fresh drain window")
	}
}
