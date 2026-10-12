package kernel

import "path/filepath"

// Shared by worker confinement and managed-environment materialization.
func pythonRuntimePackageAssets() []string {
	return []string{
		filepath.Join("x_science_runtime", "__init__.py"),
		filepath.Join("x_science_runtime", "cheminfo_render.py"),
		filepath.Join("x_science_runtime", "matplotlib_runtime.py"),
		filepath.Join("x_science_runtime", "python_code_compatibility.py"),
		filepath.Join("x_science_runtime", "worker_transport.py"),
		filepath.Join("x_science_runtime", "worker_streams.py"),
		filepath.Join("x_science_runtime", "worker_compile.py"),
		filepath.Join("x_science_runtime", "worker_process_outcomes.py"),
		filepath.Join("x_science_runtime", "worker_execution.py"),
		filepath.Join("x_science_runtime", "worker_safety.py"),
		filepath.Join("x_science_runtime", "worker_reads.py"),
		filepath.Join("x_science_runtime", "worker_effects.py"),
	}
}
