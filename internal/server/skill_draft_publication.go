package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	kernelruntime "x-science/internal/kernel"
	"x-science/internal/skills"
)

func (s *Server) publishCompatibilitySkillDraftWithCommit(name string, overwrite bool, expectedRevision string, commit skillDraftCommit) (skills.Skill, error) {
	catalogChanged := false
	defer func() {
		if catalogChanged {
			s.notifySkillCatalogChanged("")
		}
	}()
	if err := requireSkillDraftRevision(expectedRevision); err != nil {
		return skills.Skill{}, err
	}
	draft, found, err := s.findCompatibilitySkillDraft(name)
	if err != nil {
		return skills.Skill{}, err
	}
	if !found {
		return skills.Skill{}, fmt.Errorf("%w: Skill draft was not found", os.ErrNotExist)
	}
	snapshot, err := captureSkillDraftSnapshot(draft)
	if err != nil {
		return skills.Skill{}, err
	}
	if snapshot.revision != expectedRevision {
		return skills.Skill{}, kernelruntime.NewHostCallError("draft_revision_conflict", "Skill draft changed after review; read and review it again before publishing")
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
	destination, existing, err := filesystem.destination(draft.Name)
	if err != nil {
		return skills.Skill{}, err
	}
	destinationName := filepath.Base(destination)
	legacyDraft := sameCleanPath(root, destination)
	if published, found := findCatalogSkill(s.skillCatalog, draft.Name); found && !sameCleanPath(filepath.Dir(published.Path), root) {
		if _, err := s.compatibilityEditableSkillRoot(published); err != nil {
			return skills.Skill{}, err
		}
		if !overwrite {
			return skills.Skill{}, fmt.Errorf("%w: Skill is already published", os.ErrExist)
		}
	}
	if existing && !overwrite && !legacyDraft {
		return skills.Skill{}, fmt.Errorf("%w: Skill is already published", os.ErrExist)
	}
	staged, err := stageSkillDraftSnapshot(filesystem, snapshot)
	if err != nil {
		return skills.Skill{}, err
	}
	defer staged.cleanup(filesystem)
	installed := staged.skill
	installed.Name = destinationName
	installed.Path = filepath.Join(destination, "SKILL.md")
	// Keep the catalog projection with the guarded filesystem promotion, as
	// imports do. An uncertain TX acknowledgement must not hide installed bytes.
	// Validation, snapshots and author cleanup stay outside the database TX.
	if err := commit(func() error {
		if err := installSkillDirectoryAtomic(filesystem, staged.source, destination, existing); err != nil {
			return err
		}
		s.skillCatalog.UpsertSkill(installed)
		catalogChanged = true
		return nil
	}); err != nil {
		return skills.Skill{}, err
	}
	if !legacyDraft {
		cleanupReviewedSkillDraft(filesystem, draft, authorIdentity, snapshot.revision)
	}
	return installed, nil
}

func cleanupReviewedSkillDraft(filesystem *skillImportFilesystem, draft skills.Skill, authorIdentity os.FileInfo, revision string) {
	root := filepath.Dir(draft.Path)
	current, inspectErr := filesystem.lstat(root)
	if inspectErr != nil || !os.SameFile(current, authorIdentity) {
		log.Printf("Skill publication retained an author draft whose identity changed")
		return
	}
	// Preserve a changed author copy even if an out-of-band edit retained its inode.
	remaining, readErr := captureSkillDraftSnapshot(draft)
	if readErr != nil || remaining.revision != revision {
		log.Printf("Skill publication retained an author draft whose content changed")
		return
	}
	if err := filesystem.removeOwnedDirectory(root, authorIdentity); err != nil {
		log.Printf("Skill publication retained its author draft after cleanup failure")
	}
}

func (s *Server) publishCompatibilitySkillDraft(name string, overwrite bool, expectedRevision string) (skills.Skill, error) {
	return s.publishCompatibilitySkillDraftWithCommit(name, overwrite, expectedRevision, immediateSkillDraftCommit)
}
