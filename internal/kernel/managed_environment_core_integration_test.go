package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in installed-runtime evidence: reads the existing immutable generation
// and runs import witnesses, without provisioning, changing pointers, or
// borrowing task databases. Ordinary CI has no host installation authority.
func TestInstalledCoreEnvironmentPreflightAndImports(t *testing.T) {
	root := os.Getenv("SYNON_TEST_INSTALLED_CONDA_ENVS")
	if root == "" {
		t.Skip("an explicit installed-runtime root is required")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("installed-runtime root must be absolute")
	}
	for _, test := range []struct {
		language string
		config   Config
		imports  []string
	}{
		{"python", bundledManagedPythonConfig(t), []string{"rdkit", "numpy"}},
		{"r", bundledManagedRConfig(t), []string{"data.table", "jsonlite"}},
	} {
		t.Run(test.language, func(t *testing.T) {
			config := test.config
			config.CondaEnvsPath = root
			config.CondaHome = filepath.Dir(root)
			config.PythonHelperPath = filepath.Join(repositoryRootForCondaRuntimeTest(t), "assets", "optional", "kernels", "cheminfo_render_helpers.py")
			manager := NewManager(config)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			name := manager.ManagedPythonEnvironmentName()
			if test.language == "r" {
				name = managedRName(config)
			}
			before, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			value, found, err := manager.InspectManagedEnvironment(ctx, name)
			if err != nil || !found || value.Status != "ready" || value.Generation == "" || value.Language != test.language {
				t.Fatalf("installed core preflight=%#v found=%t err=%v", value, found, err)
			}
			if err := manager.VerifyManagedEnvironmentImports(ctx, name, test.imports); err != nil {
				t.Fatalf("real installed import witness: %v", err)
			}
			after, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(before) != string(after) {
				t.Fatal("read-only preflight changed the active pointer")
			}
		})
	}
}
