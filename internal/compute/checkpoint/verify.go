package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"synon-go/internal/compute/transfer"
)

func Verify(ctx context.Context, root, privateDirectory string, contract Contract, inputSHA string) (receipt Receipt, resultErr error) {
	if !filepath.IsAbs(root) {
		return receipt, errors.New("checkpoint root is not absolute")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return receipt, errors.New("checkpoint root authority changed")
	}
	manifestPath := filepath.Join(root, filepath.FromSlash(contract.Manifest))
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() {
		return receipt, errors.New("native checkpoint manifest is not a regular file")
	}
	resolved, err = filepath.EvalSymlinks(manifestPath)
	if err != nil || resolved != manifestPath {
		return receipt, errors.New("native checkpoint manifest uses a path alias")
	}
	manifest, err := os.Open(manifestPath)
	if err != nil {
		return receipt, err
	}
	defer manifest.Close()
	index, err := transfer.NewPathIndex(ctx, privateDirectory)
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, index.Close()) }()
	return Read(ctx, manifest, inputSHA, contract.CommandSHA256(), func(file File) error {
		if file.Path == contract.Manifest {
			return errors.New("checkpoint cannot include its own manifest payload")
		}
		if err := index.Add(ctx, file.Path); err != nil {
			return err
		}
		location := filepath.Join(root, filepath.FromSlash(file.Path))
		resolved, err := filepath.EvalSymlinks(location)
		if err != nil || resolved != location {
			return errors.New("checkpoint payload path changed or escaped")
		}
		return transfer.VerifyFile(ctx, location, file.SHA256, file.Bytes)
	})
}
