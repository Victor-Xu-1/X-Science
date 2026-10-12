package productstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func receiptFixture(t *testing.T) (string, string, namespaceReceipt) {
	t.Helper()
	root := t.TempDir()
	privateFixture(t, root, ".synon/teams/team.json")
	sources, err := inspectSources(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(root, HiddenDirectory)
	if err := os.Mkdir(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(canonical, "namespace-upgrade.pending.json"), namespaceReceipt{Version: 1, Sources: sources}
}

func TestHiddenNamespaceInterruptedReceiptStageDoesNotBlockSameSourceRetry(t *testing.T) {
	root, target, proof := receiptFixture(t)
	var orphan string
	injected := errors.New("injected write interruption")
	writer := func(path string, _ []byte, mode os.FileMode) error {
		orphan = path
		if err := writePrivateFile(path, []byte(`{"version":`), mode); err != nil {
			return err
		}
		return injected
	}
	if err := publishPendingReceipt(root, target, proof, writer); !errors.Is(err, injected) {
		t.Fatal("injected failure lost")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("partial stage became a pending authority")
	}
	if err := UpgradeHiddenNamespace(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(orphan); err != nil || string(raw) != `{"version":` {
		t.Fatal("orphan recovery staging was reclaimed or changed")
	}
	if raw, err := os.ReadFile(filepath.Join(root, ".synon/teams/team.json")); err != nil || string(raw) != "fixture unchanged user bytes" {
		t.Fatal("source recovery changed")
	}
}

func TestHiddenNamespaceIncompleteReceiptReadbackAndLateCollisionStayUnconfirmed(t *testing.T) {
	for _, kind := range []string{"incomplete", "collision"} {
		t.Run(kind, func(t *testing.T) {
			root, target, proof := receiptFixture(t)
			writer := func(path string, raw []byte, mode os.FileMode) error {
				if kind == "incomplete" {
					return writePrivateFile(path, []byte(`{"version":`), mode)
				}
				if err := writePrivateFile(target, []byte("foreign incomplete receipt"), mode); err != nil {
					return err
				}
				return writePrivateFile(path, raw, mode)
			}
			if err := publishPendingReceipt(root, target, proof, writer); err == nil {
				t.Fatal("unconfirmed publication succeeded")
			}
			if kind == "collision" {
				if raw, _ := os.ReadFile(target); string(raw) != "foreign incomplete receipt" {
					t.Fatal("foreign target was overwritten")
				}
				if err := UpgradeHiddenNamespace(context.Background(), root); err == nil {
					t.Fatal("truncated existing receipt was overwritten")
				}
			}
			if _, err := os.Stat(filepath.Join(root, HiddenDirectory, "teams")); !os.IsNotExist(err) {
				t.Fatal("unconfirmed receipt copied active state")
			}
		})
	}
}
