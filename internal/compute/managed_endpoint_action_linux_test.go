//go:build linux

package compute

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedEndpointOwnedActionBoundsInheritedOutputLifetime(t *testing.T) {
	for _, kind := range []string{"start", "stop"} {
		t.Run(kind, func(t *testing.T) {
			output, err := os.Create(filepath.Join(t.TempDir(), "action.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			started := time.Now()
			result := runManagedEndpointOwnedAction(ctx, "printf 'owned child started\\n'; sleep 2 & wait", managedEndpointBaseEnvironment(), output)
			if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
				t.Fatalf("finite %s waited for inherited output: %s", kind, elapsed)
			}
			if !errors.Is(result.Err, context.DeadlineExceeded) || !result.Exited || result.Code == nil {
				t.Fatalf("actual exit/cause lost: %#v", result)
			}
			if _, err := output.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			text, err := io.ReadAll(output)
			if err != nil || !strings.Contains(string(text), "owned child started") {
				t.Fatalf("output=%q err=%v", text, err)
			}
		})
	}
}

func TestManagedEndpointSuccessfulActionKeepsLateWriterAlive(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	late := make(chan string, 1)
	go func() {
		var text strings.Builder
		buffer := make([]byte, 128)
		for {
			n, err := reader.Read(buffer)
			text.Write(buffer[:n])
			if strings.Contains(text.String(), "late daemon output") {
				late <- text.String()
				return
			}
			if err != nil {
				late <- text.String()
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := runManagedEndpointOwnedAction(ctx, "printf 'accepted\\n'; (sleep 0.25; printf 'late daemon output\\n') &", managedEndpointBaseEnvironment(), writer)
	if result.Err != nil || !result.Exited || result.Code == nil || *result.Code != 0 {
		t.Fatalf("shell exit=%#v", result)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("shell exit waited for daemon: %s", elapsed)
	}
	select {
	case text := <-late:
		if !strings.Contains(text, "accepted") || !strings.Contains(text, "late daemon output") {
			t.Fatalf("late writer was lost: %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("successful daemon output was closed or killed after shell exit")
	}
}

func TestManagedEndpointOwnedActionUsesExplicitEnvironmentAndReportsFailure(t *testing.T) {
	t.Setenv("UNAPPROVED_WEB_TOKEN", "must-not-be-inherited")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	output, err := os.Create(filepath.Join(t.TempDir(), "action.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	result := runManagedEndpointOwnedAction(context.Background(), "if test -n \"$UNAPPROVED_WEB_TOKEN$HTTP_PROXY\"; then exit 99; fi; exit 7", managedEndpointBaseEnvironment(), output)
	if result.Err == nil || !result.Exited || result.Code == nil || *result.Code != 7 {
		t.Fatalf("ordinary failure was hidden: %#v", result)
	}
	if executable, err := exec.LookPath("curl"); err == nil {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) }))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result = runManagedEndpointOwnedAction(ctx, executable+" -fsS -X POST '"+server.URL+"'", managedEndpointBaseEnvironment(), output)
		if result.Err != nil || calls.Load() != 1 {
			t.Fatalf("approved loopback call calls=%d exit=%#v", calls.Load(), result)
		}
	}
}
