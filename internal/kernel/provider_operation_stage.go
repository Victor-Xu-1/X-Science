package kernel

import (
	"errors"
	"os"
	"path/filepath"
)

func prepareProviderOperationStage(recorded string) (string, func(), error) {
	if recorded == "" {
		stage, err := os.MkdirTemp("/tmp", providerOperationStagePrefix)
		if err != nil {
			return "", nil, err
		}
		return stage, func() { _ = os.RemoveAll(stage) }, nil
	}
	if !filepath.IsAbs(recorded) || filepath.Clean(recorded) != recorded || recorded == "/" {
		return "", nil, errors.New("provider operation durable stage is invalid")
	}
	resolved, err := filepath.EvalSymlinks(recorded)
	info, statErr := os.Lstat(recorded)
	if err != nil || statErr != nil || resolved != recorded || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", nil, errors.New("provider operation durable stage is not a private canonical directory")
	}
	return recorded, func() {}, nil
}

func stageProviderOperationRequest(stage string, request []byte) error {
	// Remove only known regular metadata from the previous serialized call.
	// Never follow a stale worker-created link or accept its previous reply.
	for _, name := range []string{"req.json", "reply.json"} {
		location := filepath.Join(stage, name)
		if info, err := os.Lstat(location); err == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
				return errors.New("provider operation metadata is not private and regular")
			}
			if err := os.Remove(location); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	file, err := os.OpenFile(filepath.Join(stage, "req.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(request)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
