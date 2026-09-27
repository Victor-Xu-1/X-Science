package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPythonWorkerNamespaceAndMissingStateRecovery(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	cells := []string{
		"import json, os\nprior_result = {'records': [7]}\nassert os.path.basename('example') == 'example'\nprint(json.dumps(prior_result))",
		"assert prior_result.get('records') == [7]\nprint(json.dumps(prior_result))",
		"import json as encoder\nfrom collections import Counter as Counts, defaultdict as Mapping\nassert encoder.loads('{}') == {}\nassert Counts('aa')['a'] == 2\nassert Mapping(int)['a'] == 0",
		"def read(json):\n return json.get('value')\nassert read({'value': 7}) == 7",
		"globals()['record'] = {'value': 7}\nassert record.get('value') == 7",
		"read = lambda factory=dict(a=1), record=None: record.get('value')\nassert read(record={'value': 7}) == 7",
		"def read(\n factory=dict(a=[1,2]),\n record=None,\n):\n return record.get('value')\nassert read(record={'value': 7}) == 7",
	}
	for index, code := range cells {
		outcome := executeHostCallCell(t, manager, fmt.Sprintf("exec-namespace-%d", index), code, nil)
		if outcome.Err != nil || outcome.Response.Error != "" || len(outcome.Response.Preflight) != 0 {
			t.Fatalf("valid cell %d failed: %#v", index, outcome)
		}
	}

	// A different real worker has no previous variables. Its NameError is an
	// execution failure, not a fabricated non-executing admission result.
	fresh, _ := newHostCallTestSession(t, nil)
	missing := executeHostCallCell(t, fresh, "exec-state-lost", "side_effects = ['once']\nprint(prior_result.get('records'))\nside_effects.append('must-not-run')", nil)
	if missing.Err != nil || !strings.Contains(missing.Response.Error, "NameError") || len(missing.Response.Preflight) != 0 {
		t.Fatalf("lost namespace outcome=%#v", missing)
	}
	recovered := executeHostCallCell(t, fresh, "exec-state-repaired", "assert side_effects == ['once']\nprior_result = {'records': [7]}\nassert prior_result.get('records') == [7]\nassert side_effects == ['once']", nil)
	if recovered.Err != nil || recovered.Response.Error != "" || len(recovered.Response.Preflight) != 0 {
		t.Fatalf("targeted state recovery failed: %#v", recovered)
	}
}

func TestWorkerUsesPythonRuntimeErrorsInsteadOfDomainPreflight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kernel process confinement is unavailable on Windows")
	}
	manager := newLifecycleTestManager(t, Config{})
	workspaceDir := t.TempDir()
	if _, err := manager.StartSession(SessionSpec{
		KernelID: "kernel-runtime-contract", FrameID: "frame-runtime-contract",
		RootFrameID: "root-runtime-contract", AgentName: "OPERON",
		Language: "python", Environment: "python", WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(workspaceDir, "analysis.py")
	if err := os.WriteFile(scriptPath, []byte(`from pathlib import Path

def score(left, right):
    return left + right

value = score(1, 2, 3)
Path("must-not-run").write_text(str(value))
`), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := `source = open("analysis.py", encoding="utf-8").read()
exec(compile(source, "analysis.py", "exec"))`
	first, err := manager.Submit(SubmitRequest{
		KernelID: "kernel-runtime-contract", FrameID: "frame-runtime-contract",
		Language: "python", Environment: "python", ExecID: "exec-runtime-invalid",
		ToolUseID: "tool-runtime-invalid", ToolName: "python", Origin: "agent", Code: wrapper,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid := waitForLifecycleOutcome(t, first)
	if invalid.Err != nil || !strings.Contains(invalid.Response.Error, "TypeError") || len(invalid.Response.Preflight) != 0 {
		t.Fatalf("Python runtime failure=%#v", invalid)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "must-not-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime continued after the failing expression: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte(`from pathlib import Path

def score(left, right):
    return left + right

Path("runtime-ran").write_text(str(score(1, 3)))
`), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Submit(SubmitRequest{
		KernelID: "kernel-runtime-contract", FrameID: "frame-runtime-contract",
		Language: "python", Environment: "python", ExecID: "exec-runtime-valid",
		ToolUseID: "tool-runtime-valid", ToolName: "python", Origin: "agent", Code: wrapper,
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := waitForLifecycleOutcome(t, second)
	if valid.Err != nil || valid.Response.Error != "" || len(valid.Response.Preflight) != 0 {
		t.Fatalf("Python valid execution=%#v", valid)
	}
	if contents, err := os.ReadFile(filepath.Join(workspaceDir, "runtime-ran")); err != nil || string(contents) != "4" {
		t.Fatalf("Python output=%q err=%v", contents, err)
	}
}
