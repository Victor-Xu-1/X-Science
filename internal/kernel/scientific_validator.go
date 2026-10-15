package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ScientificValidatorUnavailableError identifies an absent optional capability,
// not an invalid runtime generation, marker, contract, or inaccessible input.
// Its private reason is set only at positively identified absence boundaries.
type ScientificValidatorUnavailableError struct{ reason string }

func (err *ScientificValidatorUnavailableError) Error() string {
	return "optional scientific validator capability is unavailable: " + err.reason
}

func (err *ScientificValidatorUnavailableError) Reason() string { return err.reason }

func (m *Manager) ScientificArtifactValidator() (python, validator, generation, rdkitVersion string, err error) {
	if m == nil || strings.TrimSpace(m.config.CondaEnvsPath) == "" || strings.TrimSpace(m.config.CondaRuntimeCatalog) == "" || strings.TrimSpace(m.config.SDFValidatorPath) == "" {
		return "", "", "", "", &ScientificValidatorUnavailableError{reason: "not_configured"}
	}
	if _, supported := currentManagedRuntimePlatform(); !supported {
		return "", "", "", "", &ScientificValidatorUnavailableError{reason: "unsupported_platform"}
	}
	validator = strings.TrimSpace(m.config.SDFValidatorPath)
	if !filepath.IsAbs(validator) {
		return "", "", "", "", errors.New("scientific artifact validator path is invalid")
	}
	runtime, err := loadManagedPythonRuntime(m.config)
	if err != nil {
		return "", "", "", "", err
	}
	active := filepath.Join(m.config.CondaEnvsPath, runtime.entry.Name)
	if _, err := os.Lstat(active); errors.Is(err, os.ErrNotExist) {
		return "", "", "", "", &ScientificValidatorUnavailableError{reason: "runtime_not_installed"}
	} else if err != nil {
		return "", "", "", "", err
	}
	// An existing but broken activation is not equivalent to a capability that
	// has never been installed. Keep generation/marker/helper checks authoritative.
	if err := m.verifyManagedPythonGeneration(runtime, active); err != nil {
		return "", "", "", "", err
	}
	prefix, err := resolveManagedRuntimeGeneration(active)
	if err != nil {
		return "", "", "", "", err
	}
	info, err := os.Stat(validator)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", "", "", &ScientificValidatorUnavailableError{reason: "validator_not_installed"}
	}
	if err != nil {
		return "", "", "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", "", "", errors.New("scientific artifact validator is not a regular file")
	}
	python, err = managedPythonExecutableAtPrefix(prefix)
	if err != nil {
		return "", "", "", "", err
	}
	return python, validator, runtime.activationGeneration, runtime.rdkitVersion, nil
}
