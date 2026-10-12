package kernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func namespaceRegistrationFixture(t *testing.T, name string) (string, string, managedEnvironmentMarker, []byte) {
	t.Helper()
	root := t.TempDir()
	marker := managedEnvironmentMarker{SchemaVersion: managedEnvironmentMarkerVersion, ValidationRevision: managedEnvironmentValidationRevision,
		Name: name, Kind: "conda", Language: "python", Packages: []string{"python=3.11.9=h1=conda-forge"},
		Operation: "create", OperationKey: strings.Repeat("a", 64)}
	marker.Generation = previousNamespaceEnvironmentGeneration(marker)
	prefix := filepath.Join(root, ".generations", name, marker.Generation)
	for _, dir := range []string{"bin", "conda-meta", "lib"} {
		if err := os.MkdirAll(filepath.Join(prefix, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// This explicit test process only answers the real manager's bounded smoke
	// contract. It does not install packages or execute scientific work.
	probe := "#!/bin/sh\n[ \"$1\" = '-I' ] && [ \"$2\" = '-B' ] || exit 83\nprintf '%s\\n' '{\"ok\":true}'\n"
	if err := os.WriteFile(filepath.Join(prefix, "bin", "python"), []byte(probe), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"name":"python","version":"3.11.9","build":"h1","files":["bin/python"]}`)
	if err := os.WriteFile(filepath.Join(prefix, "conda-meta", "python-3.11.9-h1.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefix, "lib", "immutable-payload"), bytes.Repeat([]byte("payload-chunk"), 25000), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(prefix, filepath.Join(root, name)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return root, prefix, marker, raw
}

func TestNamespaceEnvironmentRegistrationPreservesPhysicalGenerationAndVisibility(t *testing.T) {
	for _, name := range []string{"user-scientific-env", "synon-biomed-mcp-python", "synon-pkg-user-chosen", "synon-pkg-e58a559d9a9e0387fe56b50e"} {
		t.Run(name, func(t *testing.T) {
			root, prefix, original, raw := namespaceRegistrationFixture(t, name)
			ctx := context.Background()
			imports, err := InspectManagedEnvironmentNamespaceImports(ctx, root)
			if err != nil || len(imports) != 1 {
				t.Fatalf("inspect imports=%v err=%v", imports, err)
			}
			frozen := imports[0]
			if frozen.Generation != original.Generation || frozen.SourcePrefix != prefix || !validSHA256(frozen.SourceSHA256) {
				t.Fatal("inspection did not retain actual source identity")
			}
			if frozen.NameOrigin != namespaceRegisteredNameOrigin(name) {
				t.Fatal("resource rename asserted the wrong origin")
			}
			manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
			adopted, err := manager.AdoptManagedEnvironmentNamespaceImport(ctx, frozen)
			if err != nil || adopted.Name != namespaceRegisteredCurrentName(name) || adopted.Generation != original.Generation ||
				adopted.Kind != namespaceRegisteredCondaKind || adopted.Status != "ready" || !reflect.DeepEqual(adopted.Packages, original.Packages) {
				t.Fatalf("adopted=%+v err=%v", adopted, err)
			}
			listed, err := manager.ListManagedEnvironments(ctx, ManagedEnvironmentQuery{IncludePackages: true})
			if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0], adopted) {
				t.Fatalf("adopted environment is absent from actual manager inventory: %v %v", listed, err)
			}
			gotPrefix, found, err := manager.ManagedEnvironmentRuntimePrefix(adopted.Name)
			if err != nil || !found || gotPrefix != prefix {
				t.Fatalf("runtime prefix=%q found=%v err=%v", gotPrefix, found, err)
			}
			oldInputPrefix, oldFound, err := manager.ManagedEnvironmentRuntimePrefix(name)
			if err != nil || !oldFound || oldInputPrefix != prefix {
				t.Fatal("receipt-bound original input alias lost its actual runtime")
			}
			if oldInput, found, err := manager.InspectManagedEnvironment(ctx, name); err != nil || !found || oldInput.Name != adopted.Name {
				t.Fatal("historical input alias published an old live resource identity")
			}
			if adopted.Name != name {
				if _, err := os.Lstat(filepath.Join(manager.config.CondaEnvsPath, name)); !os.IsNotExist(err) {
					t.Fatal("current registry published a duplicate old live name")
				}
			}
			witnessPrefix, _, err := manager.managedWitnessRuntime(adopted.Name)
			if err != nil || witnessPrefix != prefix {
				t.Fatal("executable/import witness lost the registered physical prefix")
			}
			executablePrefix, executable, err := manager.managedEnvironmentRuntime(adopted.Name, "python")
			if err != nil || executablePrefix != prefix || executable != filepath.Join(prefix, "bin", "python") {
				t.Fatal("worker executable lookup lost the registered physical prefix")
			}
			for _, value := range manager.runtimeEnvironment(adopted.Name, "python", "", "test-kernel") {
				if strings.HasPrefix(value, "CONDA_PREFIX=") && value != "CONDA_PREFIX="+prefix {
					t.Fatal("worker runtime environment used the metadata directory as its SDK prefix")
				}
			}
			if again, err := manager.AdoptManagedEnvironmentNamespaceImport(ctx, frozen); err != nil || !reflect.DeepEqual(again, adopted) {
				t.Fatalf("idempotent adoption=%+v err=%v", again, err)
			}
			after, err := InspectManagedEnvironmentNamespaceImports(ctx, root)
			if err != nil || !reflect.DeepEqual(after, imports) {
				t.Fatal("adoption changed the original prefix, marker or activation")
			}
			gotRaw, err := os.ReadFile(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName))
			if err != nil || !bytes.Equal(gotRaw, raw) {
				t.Fatal("original marker bytes changed")
			}
			canonicalPrefix, err := filepath.EvalSymlinks(filepath.Join(manager.config.CondaEnvsPath, adopted.Name))
			if err != nil || canonicalPrefix == prefix {
				t.Fatal("registry activation aliases the old SDK tree instead of canonical metadata")
			}
			if _, err := os.Stat(filepath.Join(canonicalPrefix, previousNamespaceEnvironmentMarkerName)); !os.IsNotExist(err) {
				t.Fatal("current registry emitted a historical marker")
			}
			if _, err := os.Stat(filepath.Join(canonicalPrefix, "bin", "python")); !os.IsNotExist(err) {
				t.Fatal("runtime prefix was copied into the current registry")
			}
		})
	}
}

func TestNamespaceEnvironmentRegistrationRejectsChangedChunkAndCollision(t *testing.T) {
	root, prefix, _, _ := namespaceRegistrationFixture(t, "user-scientific-env")
	ctx := context.Background()
	imports, err := InspectManagedEnvironmentNamespaceImports(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	file, err := os.OpenFile(filepath.Join(prefix, "lib", "immutable-payload"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("changed"), 180000); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(ctx, imports[0]); err == nil {
		t.Fatal("changed physical payload reused a frozen receipt")
	}
	imports, err = InspectManagedEnvironmentNamespaceImports(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(manager.config.CondaEnvsPath, imports[0].CurrentName)
	if err := os.WriteFile(active, []byte("existing owner data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(ctx, imports[0]); err == nil {
		t.Fatal("name collision was overwritten")
	}
	if raw, err := os.ReadFile(active); err != nil || string(raw) != "existing owner data" {
		t.Fatal("existing owner data changed on collision")
	}
}

func TestNamespaceEnvironmentRegistrationRejectsForeignMalformedAndForged(t *testing.T) {
	for _, mode := range []string{"foreign-activation", "foreign-payload-link", "malformed-marker", "malformed-package"} {
		t.Run(mode, func(t *testing.T) {
			root, prefix, marker, _ := namespaceRegistrationFixture(t, "user-scientific-env")
			switch mode {
			case "foreign-activation":
				if err := os.Remove(filepath.Join(root, marker.Name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(root, marker.Name)); err != nil {
					t.Fatal(err)
				}
			case "foreign-payload-link":
				if err := os.Symlink(t.TempDir(), filepath.Join(prefix, "foreign")); err != nil {
					t.Fatal(err)
				}
			case "malformed-marker":
				if err := os.WriteFile(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName), []byte("{broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "malformed-package":
				if err := os.WriteFile(filepath.Join(prefix, "conda-meta", "python-3.11.9-h1.json"), []byte(`{"name":"python","version":"changed","build":"h1"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root); err == nil {
				t.Fatal("unverified original source was accepted")
			}
		})
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), NamespaceEnvironmentImport{CurrentName: "forged", SourceSHA256: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("uploaded public fields minted import authority")
	}
}

func TestNamespaceEnvironmentRegistrationDoesNotPromoteOldVerification(t *testing.T) {
	root, prefix, marker, _ := namespaceRegistrationFixture(t, "unverified-user-env")
	marker.ValidationRevision = 0
	marker.Generation = previousNamespaceEnvironmentGeneration(marker)
	newPrefix := filepath.Join(root, ".generations", marker.Name, marker.Generation)
	if err := os.Rename(prefix, newPrefix); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, marker.Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newPrefix, filepath.Join(root, marker.Name)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(marker)
	if err := os.WriteFile(filepath.Join(newPrefix, previousNamespaceEnvironmentMarkerName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"name":"python","version":"3.11.9","build":"h1","files":["bin/.python-post-link.sh"]}`)
	if err := os.WriteFile(filepath.Join(newPrefix, "conda-meta", "python-3.11.9-h1.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	imports, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	adopted, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), imports[0])
	if err != nil || adopted.Status != "verification_required" {
		t.Fatalf("namespace import promoted installation verification: %+v %v", adopted, err)
	}
	if _, _, err := manager.ManagedEnvironmentRuntimePrefix(adopted.Name); err != ErrManagedEnvironmentRebuildRequired {
		t.Fatal("unverified installation was admitted as ready")
	}
}

func TestNamespaceEnvironmentRegistrationOriginalOperationRequiresExactRequest(t *testing.T) {
	oldName := "synon-biomed-mcp-python"
	root, prefix, marker, _ := namespaceRegistrationFixture(t, oldName)
	definition := struct {
		Name     string
		Packages []string
	}{oldName, []string{"requested-user-package"}}
	rawDefinition, _ := json.Marshal(definition)
	digest := sha256.Sum256(append([]byte("synon-managed-environment-operation-v1\x00create\x00"), rawDefinition...))
	marker.OperationKey = hex.EncodeToString(digest[:])
	raw, _ := json.Marshal(marker)
	if err := os.WriteFile(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	imports, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	adopted, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), imports[0])
	if err != nil {
		t.Fatal(err)
	}
	currentMarker, err := manager.activeManagedEnvironmentMarker(adopted.Name)
	if err != nil {
		t.Fatal(err)
	}
	definition.Name = adopted.Name
	ctx := previousNamespaceCreateContext(context.Background(), definition)
	ctx = manager.previousNamespaceImportedRequestContext(ctx, "", "create", definition, adopted.Name)
	if !namespaceOperationKeyMatches(ctx, currentMarker, managedEnvironmentOperationKey("create", definition)) {
		t.Fatal("canonical registry lost the original exact operation receipt")
	}
	definition.Packages = []string{"changed-user-package"}
	ctx = manager.previousNamespaceImportedRequestContext(previousNamespaceCreateContext(context.Background(), definition), "", "create", definition, adopted.Name)
	if namespaceOperationKeyMatches(ctx, currentMarker, managedEnvironmentOperationKey("create", definition)) {
		t.Fatal("changed request reused an original installer side effect")
	}
	currentMarker.NamespaceImport.SourceSHA256 = strings.Repeat("0", 64)
	if namespaceOperationKeyMatches(ctx, currentMarker, strings.Repeat("b", 64)) {
		t.Fatal("forged imported source minted recovery authority")
	}
}
