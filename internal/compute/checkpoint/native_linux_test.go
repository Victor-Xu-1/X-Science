//go:build linux

package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeProviderDeadlineSignalsOnlyTheDeclaredApplicationAndRestoresItsFiles(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	root := t.TempDir()
	contract := Contract{Manifest: "out/native.jsonl", ResumeCommand: "python3 native_worker.py --restore out/state.bin", Signal: "USR1", PIDFile: "out/native.pid"}
	for name, origin := range map[string]string{"wrapper.sh": filepath.Join(repository, "assets/optional/compute/wrapper.sh.tmpl"), "native_worker.py": filepath.Join(filepath.Dir(source), "testdata/native_worker.py")} {
		body, err := os.ReadFile(origin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"run.sh": "#!/bin/bash\npython3 native_worker.py\n", ".job_env": "", "_synon_checkpoint.env": "export OPERON_CHECKPOINT_SIGNAL=USR1\nexport OPERON_CHECKPOINT_PID_FILE=out/native.pid\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	input := strings.Repeat("a", 64)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", filepath.Join(root, "wrapper.sh"))
	command.Dir = root
	command.Env = append(os.Environ(), "OPERON_JOB_TIMEOUT_S=0", "OPERON_SANDBOX_REMAINING_S=6", "OPERON_SANDBOX_DEADLINE_EPOCH="+strconv.FormatInt(time.Now().Unix()+6, 10), "OPERON_HARVEST_MARGIN_S=4", "OPERON_TERM_GRACE_S=1", "OPERON_OUTPUT_PROTOCOL=2", "OPERON_INPUT_SHA256="+input, "OPERON_CHECKPOINT_GENERATION=1", "OPERON_CHECKPOINT_MANIFEST="+contract.Manifest, "OPERON_RESUME_COMMAND_SHA256="+contract.CommandSHA256())
	if output, err := command.CombinedOutput(); err != nil {
		log, _ := os.ReadFile(filepath.Join(root, "stderr.log"))
		t.Fatalf("native wrapper: %v\n%s\n%s", err, output, log)
	}
	receipt, err := Verify(ctx, root, t.TempDir(), contract, input)
	if err != nil || receipt.Generation != 1 {
		t.Fatal(receipt, err)
	}
	restore := exec.CommandContext(ctx, "python3", filepath.Join(root, "native_worker.py"), "--restore", "out/state.bin")
	restore.Dir = root
	restore.Env = append(os.Environ(), "OPERON_INPUT_SHA256="+input, "OPERON_CHECKPOINT_GENERATION=2", "OPERON_CHECKPOINT_MANIFEST="+contract.Manifest, "OPERON_RESUME_COMMAND_SHA256="+contract.CommandSHA256())
	if output, err := restore.CombinedOutput(); err != nil {
		t.Fatalf("native restore: %v %s", err, output)
	}
	result, err := os.ReadFile(filepath.Join(root, "out/result.txt"))
	if err != nil || string(result) != "18" {
		t.Fatalf("native state was not restored: %q %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "out.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("new wrapper packaged all outputs before host selection")
	}
}
