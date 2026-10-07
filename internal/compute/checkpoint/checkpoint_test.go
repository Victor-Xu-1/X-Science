package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkpointFixture(t *testing.T) (string, Contract, string, []byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	contract := Contract{Manifest: "out/checkpoint.jsonl", ResumeCommand: "native-worker --resume out/state.bin", Signal: "TERM"}
	input := strings.Repeat("a", 64)
	payload := []byte("native state 17")
	if err := os.WriteFile(filepath.Join(root, "out/state.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	var manifest bytes.Buffer
	encoder := json.NewEncoder(&manifest)
	for _, record := range []any{Header{Schema: Schema, Generation: 3, SourceInputSHA256: input, ResumeCommandSHA256: contract.CommandSHA256()}, File{Path: "out/state.bin", SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Bytes: int64(len(payload))}, Commit{Commit: "complete", FileCount: 1, Bytes: int64(len(payload))}} {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, contract.Manifest), manifest.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, contract, input, manifest.Bytes()
}

func TestNativeCheckpointRequiresCompleteIdentityAndActualPayloadIntegrity(t *testing.T) {
	root, contract, input, raw := checkpointFixture(t)
	receipt, err := Verify(context.Background(), root, t.TempDir(), contract, input)
	if err != nil || receipt.Generation != 3 || receipt.FileCount != 1 {
		t.Fatal(receipt, err)
	}
	for name, body := range map[string][]byte{"partial": raw[:bytes.LastIndex(raw, []byte(`{"commit"`))], "different input": bytes.ReplaceAll(raw, []byte(input), []byte(strings.Repeat("b", 64))), "after commit": append(append([]byte{}, raw...), []byte("{}\n")...), "duplicate field": bytes.Replace(raw, []byte(`"generation":3`), []byte(`"generation":1,"generation":3`), 1)} {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(context.Background(), bytes.NewReader(body), input, contract.CommandSHA256(), nil); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, "out/state.bin"), []byte("changed state17"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), root, t.TempDir(), contract, input); err == nil {
		t.Fatal("changed native checkpoint payload accepted")
	}
}

func TestNativeCheckpointRejectsTraversalAliasesAndDuplicatePaths(t *testing.T) {
	root, contract, input, raw := checkpointFixture(t)
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	duplicate := bytes.Join([][]byte{lines[0], lines[1], lines[1], []byte(`{"commit":"complete","file_count":2,"bytes":30}`), {}}, []byte("\n"))
	if err := os.WriteFile(filepath.Join(root, contract.Manifest), duplicate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), root, t.TempDir(), contract, input); err == nil {
		t.Fatal("duplicate native checkpoint payload accepted")
	}
	if err := os.WriteFile(filepath.Join(root, contract.Manifest), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside", "out/../outside", "/out/state", "out//state"} {
		body := bytes.ReplaceAll(raw, []byte("out/state.bin"), []byte(name))
		if _, err := Read(context.Background(), bytes.NewReader(body), input, contract.CommandSHA256(), nil); err == nil {
			t.Fatal("unsafe checkpoint path accepted", name)
		}
	}
	if err := os.Remove(filepath.Join(root, "out/state.bin")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(outside, []byte("native state 17"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out/state.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), root, t.TempDir(), contract, input); err == nil {
		t.Fatal("checkpoint symlink accepted")
	}
}
