package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNamespaceEnvironmentRegistrationUnresolvedDependencyNeverGrantsTarget(t *testing.T) {
	root, prefix, _, _ := namespaceRegistrationFixture(t, "user-scientific-env")
	target := filepath.Join(t.TempDir(), "absent-external-dependency")
	link := filepath.Join(prefix, "external-dependency")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	imports, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root)
	if err != nil || len(imports[0].source.UnresolvedDependencies) != 1 {
		t.Fatalf("missing explicit unresolved fact: %v", err)
	}
	fact := imports[0].source.UnresolvedDependencies[0]
	if !fact.ObservedAbsent || fact.LiteralTarget != target || fact.RelativePath != "external-dependency" || !validSHA256(fact.SHA256) {
		t.Fatal("unresolved target was not recorded truthfully")
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	if err := manager.PreflightManagedEnvironmentNamespaceImports(context.Background(), imports); err != nil {
		t.Fatal(err)
	}
	adopted, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), imports[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ManagedEnvironmentRuntimePrefix(adopted.Name); err == nil {
		t.Fatal("now-live external target silently gained runtime trust")
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), imports[0]); err == nil {
		t.Fatal("dependency drift reused the frozen source observation")
	}
}

func TestNamespaceEnvironmentRegistrationRejectsRelativeDanglingAndCyclicLinks(t *testing.T) {
	for _, mode := range []string{"relative-escape", "cyclic"} {
		t.Run(mode, func(t *testing.T) {
			root, prefix, _, _ := namespaceRegistrationFixture(t, "user-scientific-env")
			target := "../../../../absent-external"
			if mode == "cyclic" {
				target = "untrusted-link"
			}
			if err := os.Symlink(target, filepath.Join(prefix, "untrusted-link")); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root); err == nil {
				t.Fatal("unproven link escape or cycle was admitted")
			}
		})
	}
}

func TestNamespaceEnvironmentRegistrationPreflightPreventsKnownPartialCollision(t *testing.T) {
	firstRoot, _, _, _ := namespaceRegistrationFixture(t, "first-user-env")
	secondRoot, _, _, _ := namespaceRegistrationFixture(t, "second-user-env")
	ctx := context.Background()
	first, err := InspectManagedEnvironmentNamespaceImports(ctx, firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := InspectManagedEnvironmentNamespaceImports(ctx, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	imports := append(first, second...)
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	if err := os.WriteFile(filepath.Join(manager.config.CondaEnvsPath, second[0].CurrentName), []byte("existing user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.PreflightManagedEnvironmentNamespaceImports(ctx, imports); err == nil {
		t.Fatal("known second-resource collision passed batch preflight")
	}
	if _, err := os.Lstat(filepath.Join(manager.config.CondaEnvsPath, first[0].CurrentName)); !os.IsNotExist(err) {
		t.Fatal("preflight partially published the first environment")
	}
	if err := manager.PreflightManagedEnvironmentNamespaceImports(ctx, append(first, first...)); err == nil {
		t.Fatal("duplicate current resource identity passed batch preflight")
	}
}

func TestNamespaceEnvironmentRegistrationPreflightRejectsAliasedRegistry(t *testing.T) {
	root, _, _, _ := namespaceRegistrationFixture(t, "user-scientific-env")
	imports, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	foreign := t.TempDir()
	if err := os.Symlink(foreign, filepath.Join(manager.config.CondaEnvsPath, ".generations")); err != nil {
		t.Fatal(err)
	}
	if err := manager.PreflightManagedEnvironmentNamespaceImports(context.Background(), imports); err == nil {
		t.Fatal("aliased registry parent passed preflight")
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), imports[0]); err == nil {
		t.Fatal("aliased registry parent admitted a publication")
	}
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatal("rejected adoption wrote into a foreign registry")
	}
}

func TestNamespaceEnvironmentRegistrationMissingTargetBelowAliasHasNoEffects(t *testing.T) {
	root, _, _, _ := namespaceRegistrationFixture(t, "user-scientific-env")
	imports, err := InspectManagedEnvironmentNamespaceImports(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	parent, foreign := t.TempDir(), t.TempDir()
	alias := filepath.Join(parent, "foreign-parent")
	if err := os.Symlink(foreign, alias); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{CondaEnvsPath: filepath.Join(alias, "absent-child", "envs")})
	if err := manager.PreflightManagedEnvironmentNamespaceImports(context.Background(), imports); err == nil {
		t.Fatal("missing child below a foreign parent passed preflight")
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), imports[0]); err == nil {
		t.Fatal("missing child below a foreign parent passed adoption")
	}
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatal("alias rejection still created a child in the foreign tree")
	}
}
