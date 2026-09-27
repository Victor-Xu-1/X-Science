package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
)

func TestKernelCapturedSubprocessFailurePersistsAndAllowsCorrection(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	execute := func(code string) map[string]any {
		t.Helper()
		result, err := app.executeAgentKernelTool(ctx, identity, "python", map[string]any{
			"code": code, "environment": "python", "human_description": "Validating subprocess recovery",
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	failed := execute(`import subprocess, sys
subprocess.run([sys.executable, '-c', "import sys; print('captured-before'); print('required argument --input is missing', file=sys.stderr); sys.exit(2)"], capture_output=True, check=True, text=True)`)
	if agentruntime.ClassifyToolResult(failed) != agentruntime.ToolResultFailed || failed["exit_status"] != "error" ||
		!strings.Contains(stringValue(failed["stdout"]), "captured-before") ||
		!strings.Contains(stringValue(failed["stderr"]), "required argument --input is missing") {
		t.Fatalf("child diagnostic did not reach the failed tool receipt: %#v", failed)
	}
	corrected := execute(`completed = subprocess.run([sys.executable, '-c', "print(6 * 7)"], capture_output=True, check=True, text=True)
print(completed.stdout, end='')`)
	if agentruntime.ClassifyToolResult(corrected) != agentruntime.ToolResultSucceeded || strings.TrimSpace(stringValue(corrected["stdout"])) != "42" {
		t.Fatalf("corrected execution could not continue: %#v", corrected)
	}
	if corrected["kernel_id"] != failed["kernel_id"] {
		t.Fatal("a correctable child failure replaced the persistent kernel")
	}
	logs, err := store.ListExecutionLog(identity.access.Frame.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range logs {
		if record.ID == stringValue(failed["exec_id"]) {
			if record.ExitStatus != "error" || !strings.Contains(record.Stderr, "required argument --input is missing") {
				t.Fatalf("durable failure lost its corrective diagnostics: %#v", record)
			}
			return
		}
	}
	t.Fatal("failed execution was not retained in the real SQLite execution log")
}

func TestKernelDiscardedSubprocessFailurePersistsWithoutRestart(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	execute := func(code string) map[string]any {
		t.Helper()
		result, err := app.executeAgentKernelTool(ctx, identity, "python", map[string]any{
			"code": code, "environment": "python", "human_description": "Validating process outcome propagation",
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	kernelID := ""
	failedIDs := map[string]bool{}
	for _, code := range []string{
		"import os\nos.system('exit 7')\nprint('false-success')",
		`import subprocess, sys
subprocess.run([sys.executable, '-c', "import sys; print('child-diagnostic', file=sys.stderr); sys.exit(7)"], capture_output=True, text=True)
print('false-success')`,
	} {
		failed := execute(code)
		if agentruntime.ClassifyToolResult(failed) != agentruntime.ToolResultFailed ||
			failed["exit_status"] != "error" || !strings.Contains(stringValue(failed["stderr"]), "CalledProcessError") ||
			strings.Contains(stringValue(failed["stdout"]), "false-success") {
			t.Fatalf("discarded failed child became outer success: %#v", failed)
		}
		if kernelID == "" {
			kernelID = stringValue(failed["kernel_id"])
		} else if kernelID != stringValue(failed["kernel_id"]) {
			t.Fatal("failed child replaced the persistent worker")
		}
		failedIDs[stringValue(failed["exec_id"])] = true
	}
	recovered := execute(`import os, subprocess
try:
    os.system('exit 3')
except subprocess.CalledProcessError:
    print('fallback completed')
status = os.system('exit 2')
assert os.waitstatus_to_exitcode(status) == 2`)
	if agentruntime.ClassifyToolResult(recovered) != agentruntime.ToolResultSucceeded ||
		strings.TrimSpace(stringValue(recovered["stdout"])) != "fallback completed" ||
		kernelID != stringValue(recovered["kernel_id"]) {
		t.Fatalf("explicit recovery could not continue in the same worker: %#v", recovered)
	}
	logs, err := store.ListExecutionLog(identity.access.Frame.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range logs {
		if failedIDs[record.ID] {
			if record.ExitStatus != "error" {
				t.Fatalf("SQLite lost the executed child failure: %#v", record)
			}
			delete(failedIDs, record.ID)
		}
	}
	if len(failedIDs) != 0 {
		t.Fatalf("missing durable failed executions: %v", failedIDs)
	}
}
