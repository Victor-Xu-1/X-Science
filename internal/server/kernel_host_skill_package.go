package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"x-science/internal/skills"
)

func normalizeKernelSkillReadPath(requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		requested = "SKILL.md"
	}
	relative, err := normalizeCompatibilitySkillResourcePath(requested)
	if err != nil {
		return "", err
	}
	if kernelSkillReadControlResource(relative) {
		return "", skillValidationError("reserved_resource_path", "Skill resource path is reserved for internal publication control", nil)
	}
	return filepath.ToSlash(relative), nil
}

func kernelSkillReadControlResource(relative string) bool {
	// Draft control is root-owned; a nested file with that name is ordinary
	// author content. Import markers are reserved basenames throughout a package.
	return strings.EqualFold(relative, compatibilitySkillDraftMarker) ||
		strings.EqualFold(filepath.Base(relative), skillImportPrivateMarker) ||
		strings.EqualFold(filepath.Base(relative), skillImportPendingMarker)
}

func readKernelSkillSource(skill skills.Skill, requested string) (string, []string, error) {
	if strings.HasPrefix(skill.Path, "builtin:") {
		if requested != "SKILL.md" {
			return "", nil, skillValidationError("missing_resource", "Built-in Skill has no such resource", os.ErrNotExist)
		}
		return skill.Body, []string{"SKILL.md"}, nil
	}
	root := filepath.Dir(skill.Path)
	if err := verifyWritableCompatibilitySkillRoot(root); err != nil {
		return "", nil, skillValidationError("unsafe_package_root", "Skill package root is not a verified regular directory", err)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return "", nil, skillValidationError("unreadable_package_root", "Skill package root could not be opened safely", err)
	}
	if err := verifyOpenedGrantedDirectory(directory, []hostGrant{{Path: root, Mode: "read"}}); err != nil {
		_ = directory.Close()
		return "", nil, skillValidationError("unsafe_package_root", "Opened Skill root escaped its selected package", err)
	}
	content, files, readErr := readKernelSkillPackage(directory, requested)
	if err := errors.Join(readErr, directory.Close()); err != nil {
		return "", nil, skillValidationError("unreadable_resource", "Skill package could not be read safely", err)
	}
	return content, files, nil
}

// Both operations borrow one held directory. Publication may not replace it
// between content and inventory; callers retain the existing snapshot fence.
func readKernelSkillPackage(directory *os.Root, requested string) (string, []string, error) {
	var files []string
	if requested == "SKILL.md" {
		inventory, err := inspectCompatibilitySkillResources(directory, nil)
		if err != nil {
			return "", nil, err
		}
		files = make([]string, 0, len(inventory))
		for _, relative := range inventory {
			if !kernelSkillReadControlResource(relative) {
				files = append(files, relative)
			}
		}
	}
	content, err := readKernelSkillPackageText(directory, requested)
	if err != nil {
		return "", nil, err
	}
	return content, files, nil
}

func readKernelSkillPackageText(directory *os.Root, requested string) (string, error) {
	current := ""
	var selected os.FileInfo
	parts := strings.Split(requested, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := directory.Lstat(current)
		if err != nil {
			return "", skillValidationError("unreadable_resource", "Skill resource could not be inspected safely", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (index < len(parts)-1 && !info.IsDir()) {
			return "", skillValidationError("unsafe_resource", "Skill resource path contains an unsafe entry", nil)
		}
		selected = info
	}
	file, err := openRegularWorkspaceRootFile(directory, filepath.FromSlash(requested))
	if err != nil {
		return "", skillValidationError("unsafe_resource", "Skill resource cannot be opened as a safe regular file", err)
	}
	defer file.Close()
	if err := validateCompatibilitySkillResourceIdentity(file); err != nil {
		return "", skillValidationError("unsafe_resource", "Skill resource must be a regular file with exactly one link", err)
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(selected, info) || info.Size() < 0 || info.Size() > maxSkillArchiveBytes {
		return "", skillValidationError("unsafe_resource", "Skill resource identity or byte budget changed", err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, info.Size()+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return "", skillValidationError("unreadable_resource", "Skill resource changed while being read", err)
	}
	return string(raw), nil
}
