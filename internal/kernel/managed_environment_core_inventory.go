package kernel

import (
	"sort"
	"strings"
)

// The bundled runtime owns its catalog, portable activation pointer and
// verification contract. Project that authority into the generic read model;
// never require a second marker or mutate the bundled generation to make a
// task's package preflight work.
func (m *Manager) bundledManagedEnvironment(name string) (ManagedEnvironment, string, bool, error) {
	if strings.TrimSpace(m.config.CondaRuntimeCatalog) == "" {
		return ManagedEnvironment{}, "", false, nil
	}
	var language, generation, prefix string
	var manifest condaRuntimeManifest
	var err error
	switch name {
	case managedPythonName(m.config):
		language = "python"
		var runtime managedPythonRuntime
		prefix, runtime, err = m.managedPythonActivePrefix()
		generation, manifest = runtime.activationGeneration, runtime.manifest
	case managedRName(m.config):
		language = "r"
		var runtime managedRRuntime
		prefix, runtime, err = m.managedRActivePrefix()
		generation, manifest = runtime.activationGeneration, runtime.manifest
	default:
		return ManagedEnvironment{}, "", false, nil
	}
	if err != nil {
		return ManagedEnvironment{}, "", true, err
	}
	packages := make([]string, 0, len(manifest.Packages))
	for _, item := range manifest.Packages {
		packages = append(packages, item.Name)
	}
	sort.Strings(packages)
	return ManagedEnvironment{
		Name: name, Language: language, Kind: "bundled", Generation: generation,
		Packages: packages, Status: "ready",
	}, prefix, true, nil
}
