package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"x-science/internal/skills"
)

const compatibilitySkillDraftDirectory = ".drafts"

type skillDraftCommit func(func() error) error

func immediateSkillDraftCommit(commit func() error) error { return commit() }

func (s *Server) compatibilitySkillDraftDirectory() string {
	return filepath.Join(s.fileRoot, "skills", compatibilitySkillDraftDirectory)
}

// New drafts have one hidden namespace. Legacy marker directories remain
// readable and editable in place, but are never a new-draft destination.
func (s *Server) findCompatibilitySkillDraft(name string) (skills.Skill, bool, error) {
	if !importedSkillNamePattern.MatchString(name) || name == "." || name == ".." {
		return skills.Skill{}, false, errors.New("invalid Skill draft name")
	}
	for _, parent := range []string{s.compatibilitySkillDraftDirectory(), filepath.Join(s.fileRoot, "skills")} {
		canonical, found, err := findCaseInsensitiveSkillDestination(parent, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return skills.Skill{}, false, err
		}
		if !found {
			continue
		}
		root := filepath.Join(parent, canonical)
		if !regularDraftFile(filepath.Join(root, compatibilitySkillDraftMarker)) {
			continue
		}
		if err := verifyWritableCompatibilitySkillRoot(root); err != nil {
			return skills.Skill{}, false, err
		}
		return skills.Skill{Name: canonical, Path: filepath.Join(root, "SKILL.md")}, true, nil
	}
	return skills.Skill{}, false, nil
}

func (s *Server) compatibilityAuthorSkill(name string) (skills.Skill, bool, error) {
	if draft, found, err := s.findCompatibilitySkillDraft(name); err != nil || found {
		return draft, found, err
	}
	skill, found := findCatalogSkill(s.skillCatalog, name)
	return skill, found, nil
}

func (s *Server) compatibilitySkillDraftNames() ([]string, error) {
	names := make(map[string]string)
	for _, parent := range []string{s.compatibilitySkillDraftDirectory(), filepath.Join(s.fileRoot, "skills")} {
		entries, err := os.ReadDir(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !importedSkillNamePattern.MatchString(name) || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			root := filepath.Join(parent, name)
			if !regularDraftFile(filepath.Join(root, compatibilitySkillDraftMarker)) ||
				verifyWritableCompatibilitySkillRoot(root) != nil {
				continue
			}
			key := strings.ToLower(name)
			if _, found := names[key]; !found {
				names[key] = name
			}
		}
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func managedSkillDraftPath(path, fileRoot string) bool {
	root := filepath.Join(fileRoot, "skills")
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) == 0 {
		return false
	}
	if parts[0] == compatibilitySkillDraftDirectory {
		return true
	}
	return regularDraftFile(filepath.Join(root, parts[0], compatibilitySkillDraftMarker))
}

func withoutUnpublishedSkills(catalog *skills.Catalog, fileRoot string) *skills.Catalog {
	filtered := skills.NewCatalog()
	for _, skill := range catalog.Skills() {
		if !strings.HasPrefix(skill.Path, "builtin:") && unpublishedManagedSkillPath(skill.Path, fileRoot) {
			continue
		}
		filtered.AddSkill(skill)
	}
	for _, failure := range catalog.LoadErrors() {
		if !unpublishedManagedSkillPath(failure.Path, fileRoot) {
			filtered.AddLoadError(failure.Path, errors.New(failure.Err))
		}
	}
	return filtered
}

// A seed is prepared outside the runner transaction; only the final directory
// rename enters its commit guard. No temporary copy is execution-discoverable.
func (s *Server) createCompatibilitySkillDraft(name, initialPath, content string, source *skills.Skill, commit skillDraftCommit) (skills.Skill, error) {
	if !importedSkillNamePattern.MatchString(name) || name == "." || name == ".." {
		return skills.Skill{}, errors.New("invalid Skill draft name")
	}
	if source == nil && (!utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 || len(content) > maxSkillArchiveBytes) {
		return skills.Skill{}, errors.New("Skill draft resource is invalid text or exceeds its byte budget")
	}
	initialPath, err := normalizeCompatibilitySkillResourcePath(initialPath)
	if err != nil {
		return skills.Skill{}, err
	}
	parent := s.compatibilitySkillDraftDirectory()
	if err := ensurePrivateDirectoryForImport(parent); err != nil {
		return skills.Skill{}, err
	}
	if _, found, err := s.findCompatibilitySkillDraft(name); err != nil {
		return skills.Skill{}, err
	} else if found {
		return skills.Skill{}, fmt.Errorf("%w: Skill draft already exists", os.ErrExist)
	}
	staging, err := os.MkdirTemp(parent, ".stage-")
	if err != nil {
		return skills.Skill{}, err
	}
	defer os.RemoveAll(staging)
	sourceRoot := staging
	if source == nil {
		directory, err := os.OpenRoot(staging)
		if err != nil {
			return skills.Skill{}, err
		}
		writeErr := ensureSkillEditParent(directory, filepath.Dir(initialPath))
		if writeErr == nil {
			writeErr = writeSkillFileAtomic(directory, initialPath, []byte(content), true)
		}
		if err := errors.Join(writeErr, directory.Close()); err != nil {
			return skills.Skill{}, err
		}
	} else {
		if _, err := validateCompatibilitySkillPackage(filepath.Dir(source.Path), source.Name); err != nil {
			return skills.Skill{}, err
		}
		archive, err := workspaceSkillArchive(*source)
		if err != nil {
			return skills.Skill{}, err
		}
		filesystem, err := openSkillImportFilesystem(s.fileRoot, false)
		if err != nil {
			return skills.Skill{}, err
		}
		directory, openErr := filesystem.openDirectory(staging)
		if openErr != nil {
			filesystem.close()
			return skills.Skill{}, openErr
		}
		extractErr := extractSkillArchive(directory, archive)
		_ = directory.Close()
		filesystem.close()
		if extractErr != nil {
			return skills.Skill{}, extractErr
		}
		loaded := skills.Load([]string{staging}, runtimeSkillLoadOptions())
		if len(loaded.LoadErrors()) != 0 || len(loaded.Skills()) != 1 {
			return skills.Skill{}, errors.New("Skill draft seed is invalid")
		}
		sourceRoot = filepath.Dir(loaded.Skills()[0].Path)
		if !strings.EqualFold(source.Name, name) {
			if err := rewriteCompatibilitySkillManifestName(filepath.Join(sourceRoot, "SKILL.md"), name); err != nil {
				return skills.Skill{}, err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, compatibilitySkillDraftMarker), []byte("draft\n"), 0o600); err != nil {
		return skills.Skill{}, err
	}
	if err := validateCompatibilitySkillDraftDirectory(sourceRoot); err != nil {
		return skills.Skill{}, err
	}
	destination := filepath.Join(parent, name)
	if err := commit(func() error {
		if _, existing, err := findCaseInsensitiveSkillDestination(parent, name); err != nil {
			return err
		} else if existing {
			return os.ErrExist
		}
		return os.Rename(sourceRoot, destination)
	}); err != nil {
		return skills.Skill{}, err
	}
	return skills.Skill{Name: name, Path: filepath.Join(destination, "SKILL.md")}, nil
}

func (s *Server) ensureCompatibilitySkillDraft(name string, commit skillDraftCommit) (skills.Skill, error) {
	if draft, found, err := s.findCompatibilitySkillDraft(name); err != nil || found {
		return draft, err
	}
	published, found := findCatalogSkill(s.skillCatalog, name)
	if !found {
		return skills.Skill{}, fmt.Errorf("%w: Skill was not found", os.ErrNotExist)
	}
	if _, err := s.compatibilityEditableSkillRoot(published); err != nil {
		return skills.Skill{}, err
	}
	return s.createCompatibilitySkillDraft(published.Name, "SKILL.md", "", &published, commit)
}
