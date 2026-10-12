//go:build !windows

package mcpstdio

import (
	"os"
	"path/filepath"
	"testing"
	"x-science/internal/persistence/productstate"
)

func TestOAuthNamespaceNormalOperationsDoNotReadUnrelatedModelTrees(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, ".synon/local-speech/model")
	if err := os.MkdirAll(filepath.Dir(model), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("fixture model bytes, never executed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpgradePrivateNamespace(root); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, productstate.HiddenDirectory, "local-speech/model")
	for _, path := range []string{model, current} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(path, 0o600) })
	}
	// A full migration proof would reject/read these unrelated trees. Normal
	// OAuth use must consume only its durable receipt and canonical vault fence.
	if err := UpgradePrivateNamespace(root); err == nil {
		t.Fatal("full source proof control did not observe modified fixture")
	}
	if err := saveOAuthToken(root, "fast-path", oauthTokenCache{AccessToken: "fast-path-fixture"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if token, ok := loadOAuthAccessToken(root, "fast-path"); !ok || token != "fast-path-fixture" {
			t.Fatal("normal OAuth read repeated unrelated full tree work")
		}
	}
}

func TestOAuthNamespaceMissingCanonicalVaultNeverRebindsOriginal(t *testing.T) {
	root := t.TempDir()
	if err := saveOAuthToken(root, "source", oauthTokenCache{AccessToken: "source-fixture"}); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, productstate.HiddenDirectory, "mcp-oauth-vault")
	if err := os.MkdirAll(filepath.Join(root, ".synon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(current, filepath.Join(root, ".synon/mcp-oauth-vault")); err != nil {
		t.Fatal(err)
	}
	if err := UpgradePrivateNamespace(root); err != nil {
		t.Fatal(err)
	}
	// Remove only this test-owned migrated subtree. The original is retained.
	if err := os.RemoveAll(current); err != nil {
		t.Fatal(err)
	}
	if err := saveOAuthToken(root, "replacement", oauthTokenCache{AccessToken: "must-not-reinitialize"}); err == nil {
		t.Fatal("missing current vault was silently recreated")
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatal("original credentials were recopied into missing current vault")
	}
}
