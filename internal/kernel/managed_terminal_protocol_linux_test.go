//go:build linux

package kernel

import (
	"context"
	"encoding/gob"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestManagedOneShotPreGrantFailureNeverExecutesTarget(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		name := "eof_before_grant"
		if invalid {
			name = "invalid_request"
		}
		t.Run(name, func(t *testing.T) {
			controlRead, controlWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			receiptRead, receiptWrite, err := os.Pipe()
			if err != nil {
				_ = controlRead.Close()
				_ = controlWrite.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = controlRead.Close()
				_ = controlWrite.Close()
				_ = receiptRead.Close()
				_ = receiptWrite.Close()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			helper := exec.CommandContext(ctx, os.Args[0], oneShotHelperArgument, "0")
			helper.Env = []string{}
			helper.ExtraFiles = []*os.File{controlRead, receiptWrite}
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
			_ = controlRead.Close()
			_ = receiptWrite.Close()
			marker := filepath.Join(t.TempDir(), "must-not-execute")
			request := oneShotRequest{Path: "/bin/sh", Args: []string{"sh", "-c", "printf launched > \"$1\"", "fixture", marker}}
			if invalid {
				request.Path = ""
			}
			if err := gob.NewEncoder(controlWrite).Encode(request); err != nil {
				t.Fatal(err)
			}
			receipt, err := readOneShotReceipt(receiptRead)
			if !invalid {
				if err != nil || receipt.kind != oneShotReady {
					t.Fatalf("missing pre-execution ready: receipt=%+v error=%v", receipt, err)
				}
				_ = controlWrite.Close() // Deliberately never grant launch.
				receipt, err = readOneShotReceipt(receiptRead)
			}
			if err != nil || receipt.kind != oneShotComplete || receipt.root || receipt.failure != oneShotSetupFailure {
				t.Fatalf("pre-grant failure was not explicitly non-executing: receipt=%+v error=%v", receipt, err)
			}
			if err := helper.Wait(); err == nil || ctx.Err() != nil {
				t.Fatalf("ungranted helper did not fail promptly: %v", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target executed without a grant: %v", err)
			}
		})
	}
}

func TestManagedOneShotPreservesSignalledTargetStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := newWorkerProcessCommand(ctx, "/bin/sh", "-c", "kill -TERM $$")
	err := command.Run()
	var target *oneShotTargetExitError
	if !errors.As(err, &target) || !target.status.Signaled() || target.status.Signal() != syscall.SIGTERM {
		t.Fatalf("target signal was replaced by helper exit status: %v", err)
	}
}
