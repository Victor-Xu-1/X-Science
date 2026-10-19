package server

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"x-science/internal/skills"
)

// This new authoring-resource bound follows the fixed reference package policy.
// It is independent of whether a draft has publishable frontmatter.
const maxCompatibilitySkillResourceDepth = 8

func normalizeCompatibilitySkillResourcePath(relative string) (string, error) {
	relative = filepath.ToSlash(strings.TrimSpace(relative))
	if relative == "" || relative == "." || strings.HasPrefix(relative, "/") || relative == ".." ||
		strings.HasPrefix(relative, "../") || strings.Contains(relative, "/../") {
		return "", skillValidationError("unsafe_resource_path", "Skill resource path escaped its root", nil)
	}
	relative = filepath.Clean(filepath.FromSlash(relative))
	if relative == "." || strings.EqualFold(relative, compatibilitySkillDraftMarker) {
		return "", skillValidationError("reserved_resource_path", "Skill resource path is reserved for internal draft control", nil)
	}
	if err := validateCompatibilitySkillResourceDepth(relative, false); err != nil {
		return "", err
	}
	return relative, nil
}

func validateCompatibilitySkillResourceDepth(relative string, directory bool) error {
	if !filepath.IsLocal(relative) {
		return skillValidationError("unsafe_resource_path", "Skill resource path escaped its root", nil)
	}
	parent := relative
	if !directory {
		parent = filepath.Dir(relative)
	}
	if parent != "." && len(strings.Split(filepath.ToSlash(parent), "/")) > maxCompatibilitySkillResourceDepth {
		return skillValidationError("resource_depth_exceeded", "Skill resource exceeds its directory depth budget", nil)
	}
	return nil
}

// Borrow the caller's directory capability. Only filesystem/sidecar safety is
// checked here; incomplete or invalid SKILL.md metadata remains repairable.
func validateCompatibilitySkillDraftResources(directory *os.Root) error {
	_, err := inspectCompatibilitySkillResources(directory, func(file *os.File, relative string) error {
		if err := validateCompatibilitySkillDraftResource(file, relative); err != nil {
			return err
		}
		if err := skills.ValidatePresentationSidecar(directory, relative); err != nil {
			return skillValidationError("invalid_presentation_sidecar", "Skill presentation sidecar failed its JSON/schema safety checks", err)
		}
		return nil
	})
	return err
}

// Inspect one borrowed directory capability. Reading a package needs the same
// structural safety and bounds as authoring, not publishable frontmatter.
func inspectCompatibilitySkillResources(directory *os.Root, inspect func(*os.File, string) error) ([]string, error) {
	if directory == nil {
		return nil, skillValidationError("unreadable_package_root", "Skill resource root is unavailable", nil)
	}
	files, bytes := 0, int64(0)
	paths := make([]string, 0)
	err := fs.WalkDir(directory.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return skillValidationError("unreadable_resource", "Skill package resources could not be inspected safely", walkErr)
		}
		if err := validateCompatibilitySkillResourceDepth(relative, entry.IsDir()); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return skillValidationError("unsafe_symbolic_link", "Skill package contains a symbolic link", nil)
		}
		if entry.IsDir() {
			return nil
		}
		file, err := openRegularWorkspaceRootFile(directory, relative)
		if err != nil {
			return skillValidationError("unsafe_resource", "Skill resource cannot be opened as a safe regular file", err)
		}
		defer file.Close()
		if err := validateCompatibilitySkillResourceIdentity(file); err != nil {
			return skillValidationError("unsafe_resource", "Skill resource must be a regular file with exactly one link", err)
		}
		info, err := file.Stat()
		if err != nil {
			return skillValidationError("unreadable_resource", "Skill resource metadata could not be read safely", err)
		}
		files++
		if info.Size() < 0 || info.Size() > maxSkillArchiveBytes-bytes || files > maxSkillArchiveFiles {
			return skillValidationError("resource_budget_exceeded", "Skill package exceeds its file-count or total-byte budget", nil)
		}
		bytes += info.Size()
		if inspect != nil {
			if err := inspect(file, relative); err != nil {
				return err
			}
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func validateCompatibilitySkillDraftResource(file *os.File, relative string) error {
	if filepath.Base(relative) == "SKILL.md" {
		raw, readErr := io.ReadAll(io.LimitReader(file, maxSkillArchiveBytes+1))
		if readErr != nil {
			return skillValidationError("unreadable_skill_document", "Skill manifest could not be read safely", readErr)
		}
		if len(raw) > maxSkillArchiveBytes || !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
			return skillValidationError("invalid_manifest_text", "Skill draft manifest must be bounded UTF-8 text without NUL", nil)
		}
	}
	return nil
}

func validateCompatibilitySkillDraftDirectory(root string) error {
	if err := verifyWritableCompatibilitySkillRoot(root); err != nil {
		return err
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	return errors.Join(validateCompatibilitySkillDraftResources(directory), directory.Close())
}
