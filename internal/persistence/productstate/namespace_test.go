package productstate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func privateFixture(t *testing.T, root, relative string) string {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture unchanged user bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHiddenNamespaceCopiesOnlyOwnedChildrenAndPreservesCurrentWrites(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	source := privateFixture(t, root, ".synon/teams/team.json")
	unrelated := privateFixture(t, root, ".synon/unrelated/user.json")
	canonicalOther := privateFixture(t, root, ".x-science/unrelated/user.json")
	before, err := treeSHA256(ctx, root, filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	if err := UpgradeHiddenNamespace(ctx, root); err != nil {
		t.Fatal(err)
	}
	if got, err := CanonicalTeamFilePath(root, ".synon/teams/team.json", "team"); err != nil || got != ".x-science/teams/team.json" {
		t.Fatal("typed team record did not project canonical coordinate")
	}
	if _, err := CanonicalTeamFilePath(root, ".synon/unrelated/user.json", "team"); err == nil {
		t.Fatal("arbitrary old path gained team authority")
	}
	target := filepath.Join(root, HiddenDirectory, "teams/team.json")
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "fixture unchanged user bytes" {
		t.Fatal("copied bytes differ")
	}
	if err := os.WriteFile(target, []byte("new canonical write"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeHiddenNamespace(ctx, root); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "new canonical write" {
		t.Fatal("source replay replaced canonical write")
	}
	after, err := treeSHA256(ctx, root, filepath.Dir(source))
	if err != nil || after != before {
		t.Fatal("source recovery changed")
	}
	for _, path := range []string{unrelated, canonicalOther} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unrelated data changed")
		}
	}
}

func TestHiddenNamespaceAllSourcesPreflightBeforeEffects(t *testing.T) {
	for _, kind := range []string{"source-link", "target-collision", "competing-source"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			privateFixture(t, root, ".synon/mcp-oauth-vault/secrets/fixture")
			switch kind {
			case "source-link":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, ".synon/teams")); err != nil {
					t.Fatal(err)
				}
			case "target-collision":
				privateFixture(t, root, ".x-science/mcp-oauth-vault/secrets/foreign")
			case "competing-source":
				privateFixture(t, root, ".xscience/mcp-oauth-vault/secrets/fixture")
			}
			if err := UpgradeHiddenNamespace(context.Background(), root); err == nil {
				t.Fatal("unsafe namespace was accepted")
			}
			if _, err := os.Stat(filepath.Join(root, HiddenDirectory, "namespace-upgrade.pending.json")); !os.IsNotExist(err) {
				t.Fatal("source failure created a pending migration")
			}
		})
	}
}

func TestHiddenNamespaceInterruptedExactReceiptResumesAndTamperFails(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	privateFixture(t, root, ".synon/teams/team.json")
	sources, err := inspectSources(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(root, HiddenDirectory)
	if err := os.Mkdir(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	proof := namespaceReceipt{Version: 1, Sources: sources}
	raw, _ := json.Marshal(proof)
	if err := writePrivateFile(filepath.Join(canonical, "namespace-upgrade.pending.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeHiddenNamespace(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".synon/teams/team.json"), []byte("source tamper"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeHiddenNamespace(ctx, root); err == nil {
		t.Fatal("changed original proof was accepted")
	}
}

func TestHiddenNamespaceCancellationDoesNotCreateState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	privateFixture(t, root, ".synon/teams/team.json")
	if err := UpgradeHiddenNamespace(ctx, root); err == nil {
		t.Fatal("cancelled upgrade succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, HiddenDirectory)); !os.IsNotExist(err) {
		t.Fatal("cancelled preflight wrote state")
	}
}

func TestHiddenNamespaceAncestorAliasesFailEvenWithMissingOwnedChildren(t *testing.T) {
	for _, hasSource := range []bool{false, true} {
		outside := t.TempDir()
		realRoot := filepath.Join(outside, "nested")
		if err := os.Mkdir(realRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if hasSource {
			privateFixture(t, realRoot, ".synon/teams/team.json")
		}
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(outside, alias); err != nil {
			t.Fatal(err)
		}
		if err := UpgradeHiddenNamespace(context.Background(), filepath.Join(alias, "nested")); err == nil {
			t.Fatal("ancestor alias accepted")
		}
		for _, relative := range []string{HiddenDirectory, ".x-science.namespace-upgrade.lock"} {
			if _, err := os.Stat(filepath.Join(realRoot, relative)); !os.IsNotExist(err) {
				t.Fatal("aliased authority was mutated")
			}
		}
	}
}
