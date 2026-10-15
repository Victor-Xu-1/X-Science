package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScientificValidatorAvailabilityKeepsRuntimeIdentityFailuresHard(t *testing.T) {
	var absent *Manager
	if _, _, _, _, err := absent.ScientificArtifactValidator(); err == nil {
		t.Fatal("absent manager acquired validation authority")
	} else {
		var unavailable *ScientificValidatorUnavailableError
		if !errors.As(err, &unavailable) || unavailable.Reason() != "not_configured" {
			t.Fatalf("absent capability was not typed: %v", err)
		}
	}
	for _, name := range []string{"missing runtime", "missing validator", "valid resolver", "wrong generation", "missing marker", "wrong marker", "corrupt contract"} {
		t.Run(name, func(t *testing.T) {
			config := bundledManagedPythonConfig(t)
			config.CondaEnvsPath = t.TempDir()
			config.PythonHelperPath = filepath.Join(repositoryRootForCondaRuntimeTest(t), "assets", "optional", "kernels", "cheminfo_render_helpers.py")
			config.SDFValidatorPath = filepath.Join(t.TempDir(), "validator.py")
			if err := os.WriteFile(config.SDFValidatorPath, []byte("# controlled resolver fixture; never executed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			manager := NewManager(config)
			runtime, err := loadManagedPythonRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			generation := manager.managedPythonGenerationPath(runtime)
			if name != "missing runtime" {
				if err := os.MkdirAll(filepath.Join(generation, "bin"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(generation, "bin", "python"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := manager.installManagedPythonHelpers(runtime, generation); err != nil {
					t.Fatal(err)
				}
				if err := writeManagedRuntimeMarker(filepath.Join(generation, managedRuntimeMarkerName), manager.managedPythonMarker(runtime)); err != nil {
					t.Fatal(err)
				}
				if err := manager.activateManagedPythonGeneration(runtime); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "missing validator":
				if err := os.Remove(config.SDFValidatorPath); err != nil {
					t.Fatal(err)
				}
			case "missing marker":
				if err := os.Remove(filepath.Join(generation, managedRuntimeMarkerName)); err != nil {
					t.Fatal(err)
				}
			case "wrong marker":
				marker := manager.managedPythonMarker(runtime)
				marker.Generation = "different-generation"
				if err := writeManagedRuntimeMarker(filepath.Join(generation, managedRuntimeMarkerName), marker); err != nil {
					t.Fatal(err)
				}
			case "wrong generation":
				other := filepath.Join(filepath.Dir(generation), "different-generation")
				if err := os.MkdirAll(other, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := activateManagedRuntimeGeneration(filepath.Join(config.CondaEnvsPath, runtime.entry.Name), other); err != nil {
					t.Fatal(err)
				}
			case "corrupt contract":
				manager.config.CondaRuntimeCatalog = filepath.Join(t.TempDir(), "catalog.json")
				if err := os.WriteFile(manager.config.CondaRuntimeCatalog, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			python, validator, _, _, err := manager.ScientificArtifactValidator()
			var unavailable *ScientificValidatorUnavailableError
			optional := errors.As(err, &unavailable)
			wantOptional := name == "missing runtime" || name == "missing validator"
			if optional != wantOptional || (err == nil) != (name == "valid resolver") {
				t.Fatalf("runtime identity failure was softened: optional=%t err=%v", optional, err)
			}
			if name == "valid resolver" && (python == "" || validator != config.SDFValidatorPath) {
				t.Fatal("resolver lost its verified identity")
			}
		})
	}
}
