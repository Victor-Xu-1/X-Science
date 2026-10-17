//go:build linux

package kernel

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"x-science/internal/processsupervisor"
)

const managedTerminalHelperEnv = "X_SCIENCE_TEST_MANAGED_TERMINAL_HELPER"

type managedTerminalInvocation struct {
	ArgumentZero string
	Directory    string
	Environment  string
	Input        string
	ExtraFile    string
	NativeBytes  string
}

func TestManagedOneShotPreservesInvocationAndExit(t *testing.T) {
	for _, exitCode := range []int{0, 7} {
		t.Run(strconv.Itoa(exitCode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			directory := t.TempDir()
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
			if _, err := write.WriteString("original-extra-file"); err != nil {
				t.Fatal(err)
			}
			_ = write.Close()
			command := newWorkerProcessCommand(ctx, os.Args[0], "-test.run=^TestManagedTerminalProcessHelper$")
			command.Args[0] = "retained-argument-zero"
			command.Dir = directory
			command.Env = []string{managedTerminalHelperEnv + "=result", "X_SCIENCE_TEST_TERMINAL_EXIT=" + strconv.Itoa(exitCode),
				"X_SCIENCE_TEST_TERMINAL_VALUE=original-environment", "X_SCIENCE_TEST_TERMINAL_NATIVE=byte-\xff\xfe"}
			command.Stdin = strings.NewReader("original-input")
			command.ExtraFiles = []*os.File{read}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err = command.Run()
			if exitCode == 0 && err != nil {
				t.Fatalf("successful target failed: %v", err)
			}
			if exitCode != 0 {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != exitCode {
					t.Fatalf("target exit code was not preserved: %v", err)
				}
			}
			var actual managedTerminalInvocation
			if err := json.Unmarshal(stdout.Bytes(), &actual); err != nil {
				t.Fatalf("target stdout was not preserved: %v", err)
			}
			expected := managedTerminalInvocation{"retained-argument-zero", directory, "original-environment", "original-input", "original-extra-file", "627974652dfffe"}
			if actual != expected || stderr.String() != "original-stderr" {
				t.Fatalf("invocation contract changed: actual=%+v stderr=%q", actual, stderr.String())
			}
		})
	}
}

func TestManagedOneShotStartFailuresReturn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := newWorkerProcessCommand(ctx, filepath.Join(t.TempDir(), "missing-executable"))
	if err := command.Run(); err == nil || ctx.Err() != nil {
		t.Fatalf("target startup failure did not return promptly: error=%v context=%v", err, ctx.Err())
	}
}

func TestManagedOneShotPreservesEmptyExtraFileSlots(t *testing.T) {
	for _, occupyHelperSlot := range []bool{false, true} {
		t.Run(fmt.Sprintf("occupied_helper_slot=%t", occupyHelperSlot), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
			if _, err := write.WriteString("retained-extra-file\n"); err != nil {
				t.Fatal(err)
			}
			_ = write.Close()
			command := newWorkerProcessCommand(ctx, "/bin/sh", "-c", `
if [ -e /proc/self/fd/3 ]; then printf 'empty slot was inherited'; exit 9; fi
IFS= read -r value <&4
printf '%s' "$value"
`)
			command.ExtraFiles = []*os.File{nil, read}
			var output bytes.Buffer
			command.Stdout = &output
			if occupyHelperSlot {
				occupant, openErr := os.Open(os.DevNull)
				if openErr != nil {
					t.Fatal(openErr)
				}
				t.Cleanup(func() { _ = occupant.Close() })
				configureWorkerProcess(command.Cmd)
				process, prepareErr := prepareOneShotProcess(command)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				// Force a valid helper FD into the logical nil slot. This models
				// runtime reuse without closing or replacing a runtime-owned FD.
				// The original command/request still declares slot zero empty.
				process.helper.ExtraFiles[0] = occupant
				err = process.helper.Start()
				process.started(err)
				if err != nil {
					t.Fatal(err)
				}
				defer process.close()
				err = process.wait()
			} else {
				err = command.Run()
			}
			if err != nil || output.String() != "retained-extra-file" {
				t.Fatalf("extra-file slot identity changed: error=%v output=%q", err, output.String())
			}
		})
	}
}

func TestManagedOneShotRejectsInvalidExtraFilePresence(t *testing.T) {
	for _, test := range []struct {
		name     string
		presence []byte
	}{
		{"short", []byte{0}},
		{"long", []byte{0, 1, 0}},
		{"nonbinary", []byte{0, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			controlRead, controlWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controlRead.Close(); _ = controlWrite.Close() })
			receiptRead, receiptWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = receiptRead.Close(); _ = receiptWrite.Close() })
			helper := exec.CommandContext(ctx, os.Args[0], oneShotHelperArgument, "2")
			helper.Env = []string{}
			helper.ExtraFiles = []*os.File{nil, nil, controlRead, receiptWrite}
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controlWrite.Close(); _ = helper.Process.Kill(); _ = helper.Wait() })
			_ = controlRead.Close()
			_ = receiptWrite.Close()
			// An anonymous wire struct also compiles against the old decoder;
			// ignoring this field must fail behaviorally by returning Ready.
			request := struct {
				Path              string
				Args              []string
				ExtraFiles        int
				ExtraFilePresence []byte
			}{"/bin/sh", []string{"sh", "-c", "exit 0"}, 2, test.presence}
			if err := gob.NewEncoder(controlWrite).Encode(request); err != nil {
				t.Fatal(err)
			}
			receipt, err := readOneShotReceipt(receiptRead)
			if err != nil || receipt.kind != oneShotComplete || receipt.failure != oneShotSetupFailure || receipt.root {
				t.Fatalf("invalid extra-file presence was not rejected before grant: receipt=%+v error=%v", receipt, err)
			}
		})
	}
}

func TestManagedOneShotReturnSettlesOutputDescendants(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, holdOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("detached=%t/output=%t", detached, holdOutput), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				lifetimeRead, lifetimeWrite, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lifetimeRead.Close(); _ = lifetimeWrite.Close() })
				releaseRead, releaseWrite, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = releaseRead.Close(); _ = releaseWrite.Close() })
				pidPath := filepath.Join(t.TempDir(), "child.pid")
				command := newWorkerProcessCommand(ctx, os.Args[0], "-test.run=^TestManagedTerminalProcessHelper$", "-test.timeout=15s")
				command.Env = []string{managedTerminalHelperEnv + "=root", "X_SCIENCE_TEST_TERMINAL_PID=" + pidPath,
					"X_SCIENCE_TEST_TERMINAL_DETACHED=" + strconv.FormatBool(detached),
					"X_SCIENCE_TEST_TERMINAL_OUTPUT=" + strconv.FormatBool(holdOutput)}
				command.Stdin = releaseRead
				command.ExtraFiles = []*os.File{lifetimeRead}
				var output bytes.Buffer
				command.Stdout, command.Stderr = &output, &output
				process, err := startWorkerProcess(command)
				if err != nil {
					t.Fatal(err)
				}
				_ = lifetimeRead.Close()
				_ = releaseRead.Close()
				waited := make(chan struct{})
				var result error
				go func() {
					done := make(chan error, 1)
					go func() { done <- command.Wait() }()
					watchdog := processsupervisor.NewInactivityWatchdog(time.Minute)
					result = watchdog.Wait(ctx, command.Process.Pid, done, process.kill)
					process.close()
					close(waited)
				}()
				t.Cleanup(func() {
					cancel()
					_ = command.Process.Kill() // The retained handle, never a numeric PID.
					select {
					case <-waited:
					case <-time.After(5 * time.Second):
						t.Error("fixture root did not settle")
					}
				})
				childPID := waitForWorkerProcessChildPID(t, pidPath, 3*time.Second)
				startBytes, err := os.ReadFile(pidPath + ".start")
				if err != nil {
					t.Fatal(err)
				}
				start, err := strconv.ParseInt(string(startBytes), 10, 64)
				if err != nil || start <= 0 {
					t.Fatal("fixture child birth identity is invalid")
				}
				child, err := os.FindProcess(childPID)
				if err != nil {
					t.Fatal(err)
				}
				alive, err := ProcessIdentityAlive(int64(childPID), start)
				if err != nil || !alive {
					_ = child.Release()
					t.Fatalf("fixture child identity no longer matches: %v", err)
				}
				// Register independent cleanup before allowing the root to exit. EOF
				// ends this test's exact child even if production cleanup is broken.
				t.Cleanup(func() {
					_ = lifetimeWrite.Close()
					deadline := time.Now().Add(time.Second)
					for time.Now().Before(deadline) {
						alive, err := ProcessIdentityAlive(int64(childPID), start)
						if err == nil && !alive {
							_ = child.Release()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					_ = child.Release()
					t.Error("owned fixture child did not acknowledge lifetime EOF; its own 12s deadline remains")
				})
				peer := exec.Command("/bin/sleep", "15")
				if err := peer.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = peer.Process.Kill(); _ = peer.Wait() })
				if _, err := releaseWrite.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
				select {
				case <-waited:
				case <-time.After(5 * time.Second):
					t.Fatal("one-shot result did not return within its cleanup budget")
				}
				if err := peer.Process.Signal(syscall.Signal(0)); err != nil {
					t.Fatalf("unrelated peer was affected: %v", err)
				}
				alive, err = ProcessIdentityAlive(int64(childPID), start)
				if err != nil {
					t.Fatal(err)
				}
				if alive {
					t.Fatalf("one-shot result returned while its child remained live (wait_delay=%t, successful_result=%t)", errors.Is(result, exec.ErrWaitDelay), result == nil)
				}
			})
		}
	}
}

func TestManagedTerminalProcessHelper(t *testing.T) {
	role := os.Getenv(managedTerminalHelperEnv)
	if role == "" {
		return
	}
	if role == "result" {
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(10)
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(11)
		}
		extra := os.NewFile(3, "original-extra-file")
		value, err := io.ReadAll(extra)
		if err != nil {
			os.Exit(12)
		}
		_ = extra.Close()
		_ = json.NewEncoder(os.Stdout).Encode(managedTerminalInvocation{os.Args[0], directory,
			os.Getenv("X_SCIENCE_TEST_TERMINAL_VALUE"), string(input), string(value), fmt.Sprintf("%x", os.Getenv("X_SCIENCE_TEST_TERMINAL_NATIVE"))})
		_, _ = io.WriteString(os.Stderr, "original-stderr")
		code, _ := strconv.Atoi(os.Getenv("X_SCIENCE_TEST_TERMINAL_EXIT"))
		os.Exit(code)
	}
	if role == "child" {
		pidPath := os.Getenv("X_SCIENCE_TEST_TERMINAL_PID")
		start, err := linuxProcessStartTicks(os.Getpid())
		if err != nil || start <= 0 {
			os.Exit(2)
		}
		if err := os.WriteFile(pidPath+".start", []byte(strconv.FormatInt(start, 10)), 0o600); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(3)
		}
		finished := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(finished) }()
		select {
		case <-finished:
			os.Exit(0)
		case <-time.After(12 * time.Second):
			os.Exit(4)
		}
	}
	if role != "root" {
		os.Exit(5)
	}
	lifetime := os.NewFile(3, "fixture-lifetime")
	child := exec.Command(os.Args[0], "-test.run=^TestManagedTerminalProcessHelper$", "-test.timeout=15s")
	child.Env = []string{managedTerminalHelperEnv + "=child", "X_SCIENCE_TEST_TERMINAL_PID=" + os.Getenv("X_SCIENCE_TEST_TERMINAL_PID")}
	child.Stdin = lifetime
	if strings.EqualFold(os.Getenv("X_SCIENCE_TEST_TERMINAL_OUTPUT"), "true") {
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
	}
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: strings.EqualFold(os.Getenv("X_SCIENCE_TEST_TERMINAL_DETACHED"), "true")}
	if err := child.Start(); err != nil {
		os.Exit(6)
	}
	_ = lifetime.Close()
	if _, err := io.CopyN(io.Discard, os.Stdin, 1); err != nil {
		os.Exit(7)
	}
	os.Exit(0)
}
