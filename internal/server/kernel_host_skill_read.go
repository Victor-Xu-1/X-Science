package server

import (
	"fmt"
	"strings"

	kernelruntime "x-science/internal/kernel"
	"x-science/internal/skills"
)

// Bare names retain the authoring draft-first view. An explicit catalog identity
// selects only the published package in its declared source namespace.
func (s *Server) kernelHostReadSkill(reference, path string) (any, error) {
	path, err := normalizeKernelSkillReadPath(path)
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	namespace, name, publishedReference := strings.Cut(reference, ":")
	var skill skills.Skill
	var found, draftRead bool
	if publishedReference {
		if (namespace != "local" && namespace != "bundled") || !importedSkillNamePattern.MatchString(name) || name == "." || name == ".." {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", "Skill catalog reference is invalid")
		}
		skill, found = findCatalogSkill(s.skillCatalog, name)
		if found {
			// Publication owns the managed personal directory. Presentation path
			// labels must not reclassify a personal package as bundled.
			expected := ""
			personal := personalSkillDirectory(s.fileRoot)
			if personal != "" && pathWithinRoot(skill.Path, personal) {
				expected = "local"
			} else if s.compatibilitySkillSource(skill) == "x_science_llm" {
				expected = "bundled"
			}
			found = namespace == expected && name == skill.Name
		}
	} else {
		skill, found, err = s.findCompatibilitySkillDraft(reference)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		draftRead = found
		if !found {
			skill, found = findCatalogSkill(s.skillCatalog, reference)
		}
	}
	if !found {
		return nil, kernelruntime.NewHostCallError("not_found", fmt.Sprintf("skill %q was not found", reference))
	}
	var content string
	var files []string
	var origin string
	var draftRevision string
	read := func() error {
		if publishedReference && namespace == "local" {
			// Under the existing snapshot lock, retain the same regular-root and
			// symlink confinement required by the managed publisher.
			if _, err := s.compatibilityEditableSkillRoot(skill); err != nil {
				return err
			}
		}
		var err error
		content, files, err = readKernelSkillSource(skill, path)
		if err == nil {
			// Classify the same package selected by this protected read, never
			// a separate catalog lookup or a same-name authoring draft.
			origin = "draft"
			if !draftRead {
				origin = s.compatibilitySkillSource(skill)
			}
		}
		return err
	}
	if !draftRead {
		// Reuse the publication lock/identity fence so a replacement cannot mix
		// cached catalog identity with different package bytes during readback.
		err = s.withPublishedRuntimeSkillSnapshot(skill, read)
	} else {
		// Draft edits and publication share this mutex. Revalidate after joining
		// it, without silently changing a selected draft into a published read.
		s.skillMutationMu.Lock()
		defer s.skillMutationMu.Unlock()
		current, found, resolveErr := s.findCompatibilitySkillDraft(reference)
		if resolveErr != nil {
			return nil, kernelHostRegistryError(resolveErr)
		}
		if !found || current.Name != skill.Name || !sameCleanPath(current.Path, skill.Path) {
			return nil, kernelruntime.NewHostCallError("not_found", "Selected Skill draft changed before reading")
		}
		snapshot, snapshotErr := captureSkillDraftSnapshot(current)
		if snapshotErr != nil {
			return nil, kernelHostRegistryError(snapshotErr)
		}
		content, files, err = snapshot.read(path)
		draftRevision, origin = snapshot.revision, "draft"
	}
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	result := map[string]any{"name": skill.Name, "origin": origin, "path": path, "content": content}
	if draftRevision != "" {
		result["draft_revision"] = draftRevision
	}
	if path == "SKILL.md" || draftRevision != "" {
		result["files"] = files
	}
	return result, nil
}
