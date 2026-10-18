package server

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"x-science/internal/skills"
)

// requirePublishedRuntimeSkill is shared by explicit selection, dependency
// expansion, restored closures and tool invocations. Catalog presence alone
// cannot authorize a directory while its replacement is still pending.
func (s *Server) requirePublishedRuntimeSkill(skill skills.Skill) error {
	if unpublishedManagedSkillPath(skill.Path, s.fileRoot) {
		return selectedSkillContractUnavailable{detail: fmt.Sprintf(
			"skill %q has not completed publication", skill.Name,
		)}
	}
	return nil
}

// withPublishedRuntimeSkillSnapshot serializes only the immutable source read
// with the existing publisher. No workspace, database or model work belongs in
// snapshot. The lock-free refusal also permits a publication callback to inspect
// visibility without attempting to re-enter its own mutation mutex.
func (s *Server) withPublishedRuntimeSkillSnapshot(skill skills.Skill, snapshot func() error) error {
	if err := s.requirePublishedRuntimeSkill(skill); err != nil {
		return err
	}
	managed := strings.TrimSpace(s.fileRoot) != "" && filepath.IsAbs(s.fileRoot) &&
		pathWithinRoot(skill.Path, personalSkillDirectory(s.fileRoot))
	if managed {
		s.skillMutationMu.Lock()
		defer s.skillMutationMu.Unlock()
		if err := s.requirePublishedRuntimeSkill(skill); err != nil {
			return err
		}
		// A publication may have completed while the reader waited. Never pair
		// an old cached instruction/dependency contract with the new package.
		current, found := findCatalogSkill(s.skillCatalog, skill.Name)
		if !found || !reflect.DeepEqual(current, skill) {
			return selectedSkillContractUnavailable{detail: fmt.Sprintf(
				"skill %q changed publication after selection; select the current contract", skill.Name,
			)}
		}
	}
	return snapshot()
}

func (s *Server) snapshotPublishedAgentSkillRuntime(skill skills.Skill) (agentSkillRuntimeBundle, bool, error) {
	if strings.HasPrefix(skill.Path, "builtin:") || !strings.Contains(skill.Body, "${X_SCIENCE_SKILL_DIR}") {
		err := s.withPublishedRuntimeSkillSnapshot(skill, func() error { return nil })
		return agentSkillRuntimeBundle{}, false, err
	}
	return s.loadPublishedAgentSkillRuntimeBundle(skill)
}

// Cache diagnostics use the same publication boundary without changing their
// existing decision about whether a package's resources need inspection.
func (s *Server) loadPublishedAgentSkillRuntimeBundle(skill skills.Skill) (agentSkillRuntimeBundle, bool, error) {
	var bundle agentSkillRuntimeBundle
	var resources bool
	err := s.withPublishedRuntimeSkillSnapshot(skill, func() error {
		var err error
		bundle, resources, err = loadAgentSkillRuntimeBundle(skill)
		return err
	})
	return bundle, resources, err
}
