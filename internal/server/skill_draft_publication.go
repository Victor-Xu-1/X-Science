package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"x-science/internal/skills"
)

func (s *Server) publishCompatibilitySkillDraftWithCommit(name string, overwrite bool, commit skillDraftCommit) (skills.Skill, error) {
	draft, found, err := s.findCompatibilitySkillDraft(name)
	if err != nil {
		return skills.Skill{}, err
	}
	if !found {
		return skills.Skill{}, fmt.Errorf("%w: Skill draft was not found", os.ErrNotExist)
	}
	filesystem, err := openSkillImportFilesystem(s.fileRoot, false)
	if err != nil {
		return skills.Skill{}, err
	}
	defer filesystem.close()
	root := filepath.Dir(draft.Path)
	authorIdentity, err := filesystem.lstat(root)
	if err != nil {
		return skills.Skill{}, err
	}
	validated, err := validateCompatibilitySkillPackage(root, draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	destination, existing, err := filesystem.destination(draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	destinationName := filepath.Base(destination)
	if published, found := findCatalogSkill(s.skillCatalog, draft.Name); found && !sameCleanPath(filepath.Dir(published.Path), root) {
		if _, err := s.compatibilityEditableSkillRoot(published); err != nil {
			return skills.Skill{}, err
		}
		if !overwrite {
			return skills.Skill{}, fmt.Errorf("%w: Skill is already published", os.ErrExist)
		}
	}
	if sameCleanPath(root, destination) {
		// A legacy marker directory already occupies its publication location.
		if err := commit(func() error { return filesystem.remove(filepath.Join(root, compatibilitySkillDraftMarker), false) }); err != nil {
			return skills.Skill{}, err
		}
		s.skillCatalog.UpsertSkill(validated)
		return validated, nil
	}
	if existing && !overwrite {
		return skills.Skill{}, fmt.Errorf("%w: Skill is already published", os.ErrExist)
	}
	archive, err := workspaceSkillArchive(validated)
	if err != nil {
		return skills.Skill{}, err
	}
	staging, stagingOwner, err := filesystem.createPrivateDirectory()
	if err != nil {
		return skills.Skill{}, err
	}
	defer func() {
		if current, err := filesystem.lstat(staging); err == nil && os.SameFile(current, stagingOwner) {
			_ = filesystem.remove(staging, true)
		}
	}()
	directory, err := filesystem.openDirectory(staging)
	if err != nil {
		return skills.Skill{}, err
	}
	extractErr := extractSkillArchive(directory, archive)
	_ = directory.Close()
	if extractErr != nil {
		return skills.Skill{}, extractErr
	}
	sourceRoot := filepath.Join(staging, validated.Name)
	if err := filesystem.remove(filepath.Join(sourceRoot, compatibilitySkillDraftMarker), false); err != nil {
		return skills.Skill{}, err
	}
	installed, err := validateCompatibilitySkillPackage(sourceRoot, draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	if err := commit(func() error {
		if err := installSkillDirectoryAtomic(filesystem, sourceRoot, destination, existing); err != nil {
			return err
		}
		// Filesystem promotion has succeeded. Failed cleanup preserves the
		// author copy; it must not invite repeating an already-published write.
		current, inspectErr := filesystem.lstat(root)
		if inspectErr != nil || !os.SameFile(current, authorIdentity) {
			log.Printf("Skill publication retained an author draft whose identity changed")
		} else if err := filesystem.remove(root, true); err != nil {
			log.Printf("Skill publication retained its author draft after cleanup failure")
		}
		return nil
	}); err != nil {
		return skills.Skill{}, err
	}
	installed.Name = destinationName
	installed.Path = filepath.Join(destination, "SKILL.md")
	s.skillCatalog.UpsertSkill(installed)
	return installed, nil
}

func (s *Server) publishCompatibilitySkillDraft(name string, overwrite bool) (skills.Skill, error) {
	return s.publishCompatibilitySkillDraftWithCommit(name, overwrite, immediateSkillDraftCommit)
}
