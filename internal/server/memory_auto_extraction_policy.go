package server

import (
	"context"
	"errors"
	"fmt"

	settingsstore "x-science/internal/persistence/settings"
	workspace "x-science/internal/persistence/workspace"
)

const memoryAutoExtractionSettingKey = "memory.autoExtractionEnabled"

func (s *Server) memoryAutoExtractionEnabled(userID string) (bool, error) {
	preference, err := s.memoryAutomaticPreferences(userID)
	return preference.Enabled, err
}

func (s *Server) memoryAutoExtractionValue(setting settingsstore.Setting, found bool) (bool, error) {
	preference, err := s.decodeMemoryAutomaticPreferences(setting.Value, found)
	return preference.Enabled, err
}

// An opt-out that completes while classification is waiting revokes the pending
// automatic write. This lifecycle gate orders the original setting's sole
// setter against the final short commit, never model/classifier/repair calls.
// Do not hold the shared settings-file mutex while waiting for SQLite: Skill
// publication already writes its unrelated settings while holding that DB.
func (s *Server) withAutomaticMemoryCommit(ctx context.Context, userID string, commit func() error, decisions ...*workspace.MemoryExtractionModeDecision) error {
	if s == nil || s.settingsStore == nil {
		return errors.New("automatic memory settings are unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.memoryAutoExtractionCommitMu.RLock()
	defer s.memoryAutoExtractionCommitMu.RUnlock()
	preference, err := s.memoryAutomaticPreferences(userID)
	if err != nil {
		return err
	}
	if !preference.Enabled {
		return fmt.Errorf("%w: automatic memory extraction is disabled", workspace.ErrAgentMemoryMutationForbidden)
	}
	if len(decisions) > 1 || len(decisions) == 1 && !automaticMemoryDecisionAllowed(preference, decisions[0]) {
		return fmt.Errorf("%w: automatic Memory recovery policy changed", workspace.ErrAgentMemoryMutationForbidden)
	}
	return commit()
}

func (s *Server) setMemoryAutoExtractionEnabled(userID string, enabled bool) error {
	_, err := s.updateMemoryAutomaticPreferences(userID, &enabled, nil)
	return err
}
