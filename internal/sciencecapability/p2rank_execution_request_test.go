package sciencecapability

import (
	"os/exec"
	"strings"
	"testing"
)

func TestP2RankExecutionRequestAcceptsRegisteredCompoundExtensions(t *testing.T) {
	for _, filename := range []string{"inputs/release.tar.gz", "inputs/release.TAR.GZ", "inputs/release.tgz"} {
		_, _, _, err := NormalizeExecutionRequest(ExecutionRequest{
			ExecutionPackID: "binding-pocket-prediction.p2rank",
			Inputs:          map[string]string{"protein-structure": "inputs/protein.pdb", "p2rank-release-archive": filename},
			Parameters:      map[string]any{"method": "P2Rank"},
		})
		if err != nil {
			t.Fatalf("registered archive suffix %q was rejected: %v", filename, err)
		}
	}
	for _, filename := range []string{"inputs/release.tar.gz.exe", "inputs/release.gz", "../release.tar.gz"} {
		_, _, _, err := NormalizeExecutionRequest(ExecutionRequest{
			ExecutionPackID: "binding-pocket-prediction.p2rank",
			Inputs:          map[string]string{"protein-structure": "inputs/protein.pdb", "p2rank-release-archive": filename},
			Parameters:      map[string]any{"method": "P2Rank"},
		})
		if err == nil {
			t.Fatalf("unregistered or escaping archive path %q was admitted", filename)
		}
	}
}

func TestBundledP2RankExecutionPackBootstrapsPredictionModule(t *testing.T) {
	_, _, engine, err := NormalizeExecutionRequest(ExecutionRequest{
		ExecutionPackID: "binding-pocket-prediction.p2rank",
		Inputs:          map[string]string{"protein-structure": "inputs/protein.pdb", "p2rank-release-archive": "inputs/release.tgz"},
		Parameters:      map[string]any{"method": "P2Rank"},
	})
	if err != nil {
		t.Fatal(err)
	}
	script, _, err := ExecutionPackScript(engine.ExecutionPack)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-c", `
import sys
sys.dont_write_bytecode = True
sys.argv = ["p2rank_binding_pockets.py", "--help"]
exec(compile(sys.stdin.read(), "bundled-p2rank.py", "exec"))
`)
	command.Stdin = strings.NewReader(string(script))
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "--p2rank-archive") {
		t.Fatalf("registered modular pack failed to load: %v\n%s", err, output)
	}
}
