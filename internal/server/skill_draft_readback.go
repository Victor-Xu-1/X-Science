package server

import "x-science/internal/skills"

func (s *Server) compatibilityDraftReadback(skill skills.Skill, path string, includeContent bool) (map[string]any, error) {
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	snapshot, err := s.currentSkillDraftSnapshot(skill.Name, skill.Path)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"name": snapshot.name, "origin": "draft", "draft_revision": snapshot.revision, "files": append([]string(nil), snapshot.paths...)}
	if includeContent {
		content, _, err := snapshot.read(path)
		if err != nil {
			return nil, err
		}
		result["content"] = content
	}
	return result, nil
}
