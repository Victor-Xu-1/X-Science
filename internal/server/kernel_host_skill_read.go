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
	namespace, name, publishedReference := strings.Cut(reference, ":")
	var skill skills.Skill
	var found bool
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
			} else if compatibilitySkillSource(skill) == "x_science_llm" {
				expected = "bundled"
			}
			found = namespace == expected && name == skill.Name
		}
	} else {
		var err error
		skill, found, err = s.compatibilityAuthorSkill(reference)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
	}
	if !found {
		return nil, kernelruntime.NewHostCallError("not_found", fmt.Sprintf("skill %q was not found", reference))
	}
	var content string
	read := func() error {
		if publishedReference && namespace == "local" {
			// Under the existing snapshot lock, retain the same regular-root and
			// symlink confinement required by the managed publisher.
			if _, err := s.compatibilityEditableSkillRoot(skill); err != nil {
				return err
			}
		}
		var err error
		content, err = compatibilityCatalogSkillContent(skill, path)
		return err
	}
	var err error
	if publishedReference {
		// Reuse the publication lock/identity fence so a replacement cannot mix
		// cached catalog identity with different package bytes during readback.
		err = s.withPublishedRuntimeSkillSnapshot(skill, read)
	} else {
		err = read()
	}
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	return map[string]any{"name": skill.Name, "path": path, "content": content}, nil
}
