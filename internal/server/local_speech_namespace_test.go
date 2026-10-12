package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"x-science/internal/persistence/productstate"
)

func TestLocalSpeechNamespaceExistingSevenFilesStayReadyWithoutWork(t *testing.T) {
	root := t.TempDir()
	previous := filepath.Join(root, ".synon", "local-speech")
	files := []string{"active/runtime/bin/sherpa-onnx", "active/runtime/lib/libonnxruntime.so", "active/runtime/lib/libsherpa-onnx-c-api.so", "active/runtime/lib/libsherpa-onnx-cxx-api.so",
		filepath.Join("active/model", localSpeechModelName, "model.int8.onnx"), filepath.Join("active/model", localSpeechModelName, "bbpe.model"), filepath.Join("active/model", localSpeechModelName, "tokens.txt")}
	for _, relative := range files {
		path := filepath.Join(previous, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture raw asset, never executed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !localSpeechInstallComplete(previous) {
		t.Fatal("source seven-file fixture is incomplete")
	}
	if err := productstate.UpgradeHiddenNamespace(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runtime := newLocalSpeechRuntime(root, nil)
	status := runtime.status()
	if runtime.root != filepath.Join(root, productstate.HiddenDirectory, "local-speech") || status["ready"] != true || status["phase"] != "ready" {
		t.Fatal("migrated consumer lost existing readiness")
	}
	for _, relative := range files {
		if raw, err := os.ReadFile(filepath.Join(previous, relative)); err != nil || string(raw) != "fixture raw asset, never executed" {
			t.Fatal("original asset recovery changed")
		}
	}
}
