//go:build !windows && (!linux || !amd64)

package kernel

func platformSessionRuntimeMounts(string, string, string, ...string) ([]WorkerMount, error) {
	return nil, nil
}
