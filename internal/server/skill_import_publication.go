package server

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	kernelruntime "x-science/internal/kernel"
	"x-science/internal/skills"
)

type skillImportFailure struct {
	name string
	err  error
}

// skillDirectoryPublication records the exact directory moved by this commit.
// Rollback moves that same inode back to staging; it never deletes by skill name.
type skillDirectoryPublication struct {
	filesystem  *skillImportFilesystem
	source      string
	destination string
	backup      string
	backupRoot  string
	backupOwner os.FileInfo
	published   os.FileInfo
	previous    os.FileInfo
	pending     os.FileInfo
}

func stageSkillDirectoryPublication(filesystem *skillImportFilesystem, source, destination string, replace bool) (*skillDirectoryPublication, error) {
	if err := filesystem.verifyBinding(); err != nil {
		return nil, err
	}
	info, err := filesystem.lstat(source)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("inspect staged skill directory: %w", errors.Join(err, errors.New("regular directory required")))
	}
	entry := &skillDirectoryPublication{filesystem: filesystem, source: source, destination: destination, published: info}
	if replace {
		if filesystem.markerPresent(destination, skillImportPendingMarker) || filesystem.markerPresent(destination, skillImportPrivateMarker) {
			return nil, kernelruntime.NewHostCallError("outcome_unknown", "Existing Skill publication is unresolved; inspect retained resources before replacing it")
		}
		entry.previous, err = filesystem.lstat(destination)
		if err != nil {
			return nil, fmt.Errorf("inspect existing skill: %w", err)
		}
		if entry.previous.Mode()&os.ModeSymlink != 0 || !entry.previous.IsDir() {
			return nil, errors.New("existing skill destination is not a regular directory")
		}
		entry.backupRoot, entry.backupOwner, err = filesystem.createPrivateDirectory()
		if err != nil {
			return nil, fmt.Errorf("prepare private replacement backup: %w", err)
		}
		entry.backup = filepath.Join(entry.backupRoot, "previous")
		if err := filesystem.rename(destination, entry.backup); err != nil {
			entry.cleanupBackupContainer()
			return nil, fmt.Errorf("stage existing skill replacement: %w", err)
		}
	} else if _, err := filesystem.lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return nil, fmt.Errorf("skill destination is not available: %w", err)
	}
	if err := filesystem.rename(source, destination); err != nil {
		if entry.backup != "" {
			if restoreErr := filesystem.rename(entry.backup, destination); restoreErr != nil {
				return entry, fmt.Errorf("install replacement and restore failed: %w", errors.Join(err, restoreErr))
			}
			entry.cleanupBackupContainer()
		}
		return nil, fmt.Errorf("install skill atomically: %w", err)
	}
	return entry, nil
}

func (entry *skillDirectoryPublication) rollback() error {
	info, err := entry.filesystem.lstat(entry.destination)
	if err == nil {
		if !os.SameFile(info, entry.published) {
			return errors.New("skill rollback refused: destination no longer belongs to this publication")
		}
		if _, err := entry.filesystem.lstat(entry.source); !errors.Is(err, os.ErrNotExist) {
			return errors.New("skill rollback refused: staging path is no longer vacant")
		}
		if err := entry.filesystem.rename(entry.destination, entry.source); err != nil {
			return fmt.Errorf("return published skill to staging: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if entry.backup != "" {
		info, err := entry.filesystem.lstat(entry.backup)
		if err != nil || !os.SameFile(info, entry.previous) {
			return errors.New("skill rollback refused: replacement backup identity changed")
		}
		if err := entry.filesystem.rename(entry.backup, entry.destination); err != nil {
			return fmt.Errorf("restore previous skill: %w", err)
		}
		entry.cleanupBackupContainer()
	}
	return nil
}

func rollbackSkillPublications(journal []*skillDirectoryPublication) error {
	var failures []error
	for index := len(journal) - 1; index >= 0; index-- {
		if err := journal[index].rollback(); err != nil {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return kernelruntime.NewHostCallError("outcome_unknown", "Skill rollback could not prove or restore publication ownership; retained resources require inspection: "+err.Error())
	}
	return nil
}

func finishSkillPublications(journal []*skillDirectoryPublication) {
	for _, entry := range journal {
		if entry.backup != "" {
			info, err := entry.filesystem.lstat(entry.backup)
			if err != nil || !os.SameFile(info, entry.previous) {
				log.Printf("Skill publication retained a replacement backup whose identity could not be confirmed")
				continue
			}
			entry.cleanupBackupContainer()
		}
	}
}

func installSkillDirectoryAtomic(filesystem *skillImportFilesystem, source, destination string, replace bool) error {
	entry, err := stageSkillDirectoryPublication(filesystem, source, destination, replace)
	if err != nil {
		return err
	}
	finishSkillPublications([]*skillDirectoryPublication{entry})
	return nil
}

func (s *Server) preparedSkillDestination(prepared *preparedSkillImport) (skills.Skill, string, bool, error) {
	skill := prepared.skill
	filesystem := prepared.filesystem
	installRoot := filesystem.path
	if !sameCleanPath(installRoot, filepath.Join(s.fileRoot, "skills")) {
		return skills.Skill{}, "", false, errors.New("prepared Skill belongs to another FileRoot")
	}
	if err := filesystem.verifyBinding(); err != nil {
		return skills.Skill{}, "", false, err
	}
	source, err := filesystem.lstat(prepared.sourceRoot)
	if err != nil || prepared.sourceIdentity == nil || !os.SameFile(source, prepared.sourceIdentity) {
		return skills.Skill{}, "", false, errors.New("prepared Skill package identity changed before publication")
	}
	if current, found := findCatalogSkill(s.skillCatalog, skill.Name); found {
		if strings.HasPrefix(current.Path, "builtin:") || compatibilitySkillSource(current) == "x_science_llm" {
			return skills.Skill{}, "", false, fmt.Errorf("%w: skill %q conflicts with a bundled read-only catalog entry", os.ErrExist, skill.Name)
		}
		if sameCleanPath(filepath.Dir(current.Path), filepath.Join(installRoot, skill.Name)) && !prepared.replace {
			return skills.Skill{}, "", false, fmt.Errorf("%w: skill %q is already installed", os.ErrExist, skill.Name)
		}
	}
	destination, existing, err := filesystem.destination(skill.Name)
	if err != nil {
		return skills.Skill{}, "", false, err
	}
	name := filepath.Base(destination)
	if existing && !prepared.replace {
		return skills.Skill{}, "", false, fmt.Errorf("%w: skill %q is already installed", os.ErrExist, name)
	}
	skill.Name = name
	skill.Path = filepath.Join(destination, prepared.relativeManifest)
	return skill, destination, existing, nil
}

// publishPreparedSkillImports is the single final publication boundary for
// marketplace, path and multipart imports. record only updates local settings:
// it must not re-enter Workspace/Transcript while commit borrows their DB pool.
func (s *Server) publishPreparedSkillImports(
	prepared []*preparedSkillImport,
	commit skillDraftCommit,
	record func([]skillImportResult, []skillImportFailure) error,
) ([]skillImportResult, []skillImportFailure, error) {
	if commit == nil {
		return nil, nil, errors.New("skill import commit authority is required")
	}
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	var journal []*skillDirectoryPublication
	results := make([]skillImportResult, 0, len(prepared))
	failed := make([]skillImportFailure, 0)
	published := make([]skills.Skill, 0, len(prepared))
	committed := false
	compensate := func(cause error) error {
		rollbackErr := rollbackSkillPublications(journal)
		if rollbackErr != nil {
			// Recovery ownership is uncertain. Even deferred staging cleanup
			// must not remove an unverified or partly restored resource.
			for _, item := range prepared {
				item.retain = true
			}
		}
		return errors.Join(cause, rollbackErr)
	}
	err := commit(func() error {
		for _, item := range prepared {
			skill, destination, existing, err := s.preparedSkillDestination(item)
			if errors.Is(err, os.ErrExist) {
				failed = append(failed, skillImportFailure{name: item.skill.Name, err: err})
				continue
			}
			if err != nil {
				return compensate(err)
			}
			pending, err := item.filesystem.createMarker(item.sourceRoot, skillImportPendingMarker)
			if err != nil {
				return compensate(err)
			}
			entry, err := stageSkillDirectoryPublication(item.filesystem, item.sourceRoot, destination, existing)
			if entry != nil {
				entry.pending = pending
				journal = append(journal, entry)
			}
			if err != nil {
				return compensate(err)
			}
			published = append(published, skill)
			results = append(results, skillImportResult{
				Name: skill.Name, InstalledPath: filepath.Join("skills", skill.Name),
				Files: item.files, Description: item.description,
			})
		}
		if record != nil {
			if err := record(results, failed); err != nil {
				return compensate(err)
			}
		}
		// All required metadata writes succeeded. Failure to confirm visibility
		// cannot roll that metadata back or delete the transferred resources.
		committed = true
		for _, entry := range journal {
			if err := entry.confirmImportVisibility(); err != nil {
				for _, item := range prepared {
					item.retain = true
				}
				return kernelruntime.NewHostCallError("outcome_unknown", "Skill import metadata committed but publication visibility is unconfirmed; retained resources require inspection: "+err.Error())
			}
		}
		for _, skill := range published {
			s.skillCatalog.UpsertSkill(skill)
		}
		return nil
	})
	if err != nil {
		if committed {
			// The callback transferred the filesystem and required metadata already.
			// COMMIT acknowledgement uncertainty never authorizes compensation.
			return results, failed, fmt.Errorf("Skill resources were transferred but final acknowledgement or visibility is uncertain; inspect installed Skills before retrying: %w", err)
		}
		return nil, failed, err
	}
	finishSkillPublications(journal)
	return results, failed, nil
}
