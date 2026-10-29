//go:build linux && amd64

package kernel

// Preserve the server-resolved interpreter's standard library before an egress
// shell becomes the executable. The confinement helper cannot rediscover this
// runtime from /bin/bash, and scientific PATH/CONDA_PREFIX is a separate owner.
func platformSessionRuntimeMounts(executable, _, _ string, _ ...string) ([]WorkerMount, error) {
	if prefix := kernelPythonRuntimePrefix(executable); prefix != "" {
		return []WorkerMount{TrustedReadOnlyDirectoryMount(prefix)}, nil
	}
	return nil, nil
}
