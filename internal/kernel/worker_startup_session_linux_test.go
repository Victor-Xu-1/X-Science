//go:build linux

package kernel

import (
	"context"
	"strings"
	"testing"
	"time"

	"x-science/internal/kernelcontract/bashsource"
)

func TestBashStartupConfinedSessionUsesExistingExecutionProtocol(t *testing.T) {
	manager, _, generation := startupManagerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if _, err := manager.CloseAll(shutdown); err != nil {
			t.Error(err)
		}
	})
	spec := startupSessionFixture(generation, t.TempDir())
	session, err := manager.EnsureSession(spec)
	if err != nil {
		t.Fatal(err)
	}
	code, err := bashsource.BashPythonWrapper("printf 'confined-startup-ok\\n'")
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ExecuteSession(ctx, spec, SubmitRequest{
		ExecID: "startup-owned", ToolUseID: "startup-owned-tool", ToolName: "bash", Code: code, Origin: "agent",
	})
	if err != nil || result.Err != nil || result.Response.Error != "" || strings.TrimSpace(result.Response.Stdout) != "confined-startup-ok" {
		t.Fatalf("confined startup result=%#v error=%v", result, err)
	}
	if _, err := session.Worker.Execute(ctx, "print('not-a-command-envelope')", "agent"); err == nil {
		t.Fatal("running confined supervisor accepted arbitrary Python")
	}
}
