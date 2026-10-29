package kernel

import (
	"errors"
	"runtime"

	"x-science/internal/kernelcontract/bashsource"
)

// CanonicalBashStartup derives a capability only from the existing installed
// bundled-runtime authority. Explicit operator interpreters keep their original
// contract; discovering a capability never installs or changes a runtime.
func (m *Manager) CanonicalBashStartup() (bashsource.WorkerStartupPolicy, string) {
	_, generation, err := m.canonicalBashSupervisor()
	if err != nil {
		return "", ""
	}
	return bashsource.CanonicalBashStdlibV1, generation
}

func (m *Manager) canonicalBashSupervisor() (string, string, error) {
	if m == nil || runtime.GOOS != "linux" || !m.config.defaultPythonDiscovery || len(m.config.Environment) != 0 {
		return "", "", errors.New("verified bundled Bash supervisor is unavailable")
	}
	if err := m.Verify(); err != nil {
		return "", "", err
	}
	prefix, installed, err := m.managedPythonActivePrefix()
	if err != nil {
		return "", "", err
	}
	executable, err := managedPythonExecutableAtPrefix(prefix)
	return executable, installed.activationGeneration, err
}

func (m *Manager) sessionSupervisor(spec SessionSpec) (string, error) {
	if err := bashsource.ValidateWorkerStartup(spec.WorkerStartupPolicy, spec.SupervisorRuntimeGeneration, spec.KernelKind, spec.Language); err != nil {
		return "", err
	}
	if spec.WorkerStartupPolicy == "" {
		return m.config.Python, nil
	}
	executable, generation, err := m.canonicalBashSupervisor()
	if err != nil || generation != spec.SupervisorRuntimeGeneration {
		return "", errors.New("verified Bash supervisor generation changed before start")
	}
	return executable, nil
}

func startupWorkerArguments(workerPath string, policy bashsource.WorkerStartupPolicy) []string {
	arguments := pythonWorkerArguments(workerPath)
	if policy == bashsource.CanonicalBashStdlibV1 {
		arguments = append([]string{"-I", "-S"}, arguments...)
		arguments = append(arguments, "--x-science-startup-policy="+string(policy))
	}
	return arguments
}
