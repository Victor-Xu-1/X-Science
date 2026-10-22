package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	settingsstore "x-science/internal/persistence/settings"
	workspace "x-science/internal/persistence/workspace"
)

const memoryAutoExtractionSettingKey = "memory.autoExtractionEnabled"

func (s *Server) memoryAutoExtractionEnabled(userID string) (bool, error) {
	if s == nil || s.settingsStore == nil {
		return false, errors.New("automatic memory settings are unavailable")
	}
	setting, found, err := s.settingsStore.Get(userPreferenceSettingKey(memoryAutoExtractionSettingKey, strings.TrimSpace(userID)))
	if err != nil {
		return false, fmt.Errorf("read automatic memory setting: %w", err)
	}
	return s.memoryAutoExtractionValue(setting, found)
}

func (s *Server) memoryAutoExtractionValue(setting settingsstore.Setting, found bool) (bool, error) {
	if !found {
		return s.memoryConfig.ExtractEnabled, nil
	}
	enabled, ok := setting.Value.(bool)
	if !ok {
		return false, errors.New("automatic memory setting is invalid")
	}
	return enabled, nil
}

// An opt-out that completes while classification is waiting revokes the pending
// automatic write. This lifecycle gate orders the original setting's sole
// setter against the final short commit, never model/classifier/repair calls.
// Do not hold the shared settings-file mutex while waiting for SQLite: Skill
// publication already writes its unrelated settings while holding that DB.
func (s *Server) withAutomaticMemoryCommit(ctx context.Context, userID string, commit func() error) error {
	if s == nil || s.settingsStore == nil {
		return errors.New("automatic memory settings are unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.memoryAutoExtractionCommitMu.RLock()
	defer s.memoryAutoExtractionCommitMu.RUnlock()
	enabled, err := s.memoryAutoExtractionEnabled(userID)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w: automatic memory extraction is disabled", workspace.ErrAgentMemoryMutationForbidden)
	}
	return commit()
}

func (s *Server) setMemoryAutoExtractionEnabled(userID string, enabled bool) error {
	if s == nil || s.settingsStore == nil {
		return errors.New("automatic memory settings are unavailable")
	}
	s.memoryAutoExtractionCommitMu.Lock()
	defer s.memoryAutoExtractionCommitMu.Unlock()
	_, err := s.settingsStore.Set(
		userPreferenceSettingKey(memoryAutoExtractionSettingKey, strings.TrimSpace(userID)),
		enabled,
	)
	if err != nil {
		return fmt.Errorf("persist automatic memory setting: %w", err)
	}
	return nil
}
