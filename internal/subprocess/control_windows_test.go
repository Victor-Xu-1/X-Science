//go:build windows

package subprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWindowsControlKillReapsParentBeforeAndAfterAttach(t *testing.T) {
	for _, attach := range []bool{false, true} {
		name := "before-attach"
		if attach {
			name = "after-attach"
		}
		t.Run(name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(context.Background(), executable, "-test.run=^TestWindowsControlProcessHelper$")
			command.Env, err = BuildEnvironment("process-control test", map[string]string{"X_SCIENCE_CONTROL_PROCESS_HELPER": "1"})
			if err != nil {
				t.Fatal(err)
			}
			control, err := Prepare(command)
			if err != nil {
				t.Fatal(err)
			}
			command.SysProcAttr.HideWindow = true
			if err := command.Start(); err != nil {
				_ = control.Close()
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				_ = command.Wait()
				close(done)
			}()
			// The direct parent fallback is essential when Attach has not yet
			// happened. Cleanup uses exactly one Wait owner even after Fatal.
			t.Cleanup(func() {
				_ = control.Kill(command)
				_ = command.Process.Kill()
				_ = control.Close()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("controlled parent was not reaped during cleanup")
				}
			})
			if attach {
				if err := control.Attach(command); err != nil {
					t.Fatal(err)
				}
			}
			if err := control.Kill(command); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Fatalf("Kill: %v", err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("successful Kill left the real parent running before Attach")
			}
		})
	}
}

func TestWindowsControlProcessHelper(t *testing.T) {
	if os.Getenv("X_SCIENCE_CONTROL_PROCESS_HELPER") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}
