package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// LoadFromRoot borrows an already-authorized directory capability. The caller
// retains ownership and closes it. Enumeration and reads share that same root;
// a replacement of the display path cannot select a different filesystem tree.
func LoadFromRoot(root *os.Root, directory string, options ...LoadOptions) *Catalog {
	catalog := NewCatalog()
	if root == nil {
		catalog.AddLoadError(directory, errors.New("skill root is not open"))
		return catalog
	}
	opts := LoadOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	loadOpenedDirectory(catalog, directory, root, opts)
	catalog.mu.Lock()
	catalog.sortSkillsLocked()
	catalog.mu.Unlock()
	return catalog
}

func loadOpenedDirectory(catalog *Catalog, directory string, root *os.Root, opts LoadOptions) {
	presentation := newPresentationLoader(root, directory, catalog)
	err := fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, err error) error {
		path := filepath.Join(directory, filepath.FromSlash(relative))
		if err != nil {
			catalog.AddLoadError(path, err)
			return nil
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			catalog.AddLoadError(path, errors.New("skill manifest must be a regular file, not a symbolic link"))
			return nil
		}
		if !entry.Type().IsRegular() {
			catalog.AddLoadError(path, errors.New("skill manifest must be a regular file, not a pipe or device"))
			return nil
		}
		data, err := readOpenedSkillManifest(root, filepath.FromSlash(relative))
		if err != nil {
			catalog.AddLoadError(path, err)
			return nil
		}
		skill, err := parseSkillFile(path, data)
		if err != nil {
			catalog.AddLoadError(path, err)
			return nil
		}
		presentation.apply(&skill, relative)
		body := skill.Body
		if opts.MaxBodyBytes > 0 && len(body) > opts.MaxBodyBytes {
			if opts.RejectOversizeBody {
				catalog.AddLoadError(path, fmt.Errorf("skill body exceeds %d bytes", opts.MaxBodyBytes))
				return nil
			}
			body = truncateString(body, opts.MaxBodyBytes)
		}
		skill.Body, skill.BodyHash = body, sha1Hex(body)
		catalog.AddSkill(skill)
		return nil
	})
	if err != nil {
		catalog.AddLoadError(directory, err)
	}
	presentation.finish()
}

func readOpenedSkillManifest(root *os.Root, relative string) ([]byte, error) {
	file, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("skill manifest is not a regular file")
	}
	data, err := io.ReadAll(file)
	return data, errors.Join(err, file.Close())
}
