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
	root := filepath.Dir(draft.Path)
	validated, err := validateCompatibilitySkillPackage(root, draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	installRoot := filepath.Join(s.fileRoot, "skills")
	destinationName, existing, err := findCaseInsensitiveSkillDestination(installRoot, draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	if !existing {
		destinationName = draft.Name
	}
	destination := filepath.Join(installRoot, destinationName)
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
		if err := commit(func() error { return os.Remove(filepath.Join(root, compatibilitySkillDraftMarker)) }); err != nil {
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
	staging, err := os.MkdirTemp(s.compatibilitySkillDraftDirectory(), ".publish-")
	if err != nil {
		return skills.Skill{}, err
	}
	defer os.RemoveAll(staging)
	if err := extractSkillArchive(staging, archive); err != nil {
		return skills.Skill{}, err
	}
	sourceRoot := filepath.Join(staging, validated.Name)
	if err := os.Remove(filepath.Join(sourceRoot, compatibilitySkillDraftMarker)); err != nil {
		return skills.Skill{}, err
	}
	installed, err := validateCompatibilitySkillPackage(sourceRoot, draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	if err := commit(func() error {
		if err := installSkillDirectoryAtomic(sourceRoot, destination, existing); err != nil {
			return err
		}
		// Filesystem promotion has succeeded. Failed cleanup preserves the
		// author copy; it must not invite repeating an already-published write.
		if err := os.RemoveAll(root); err != nil {
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
