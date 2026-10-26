package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"x-science/internal/memoryconfig"
	workspace "x-science/internal/persistence/workspace"
)

// This is the original per-user automatic setting, not another config store.
type memoryAutomaticPreferences struct {
	Enabled            bool                            `json:"enabled"`
	ForkedViewRecovery memoryconfig.ForkedViewRecovery `json:"forked_view_recovery"`
}

func (s *Server) decodeMemoryAutomaticPreferences(value any, found bool) (memoryAutomaticPreferences, error) {
	if !found {
		policy := s.memoryConfig.ForkedViewRecovery.Effective()
		if !policy.Valid() {
			return memoryAutomaticPreferences{}, errors.New("automatic memory recovery policy is invalid")
		}
		return memoryAutomaticPreferences{s.memoryConfig.ExtractEnabled, policy}, nil
	}
	if enabled, ok := value.(bool); ok {
		return memoryAutomaticPreferences{enabled, memoryconfig.ForkedViewRecoveryStrict}, nil
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return memoryAutomaticPreferences{}, errors.New("automatic memory setting is invalid")
	}
	enabled, ok := fields["enabled"].(bool)
	if !ok {
		return memoryAutomaticPreferences{}, errors.New("automatic memory setting is invalid")
	}
	policy := memoryconfig.ForkedViewRecoveryStrict
	for key := range fields {
		if key != "enabled" && key != "forked_view_recovery" {
			return memoryAutomaticPreferences{}, errors.New("automatic memory setting has an unknown field")
		}
	}
	if raw, found := fields["forked_view_recovery"]; found {
		value, ok := raw.(string)
		policy = memoryconfig.ForkedViewRecovery(value)
		if !ok || !policy.Valid() {
			return memoryAutomaticPreferences{}, errors.New("automatic memory recovery policy is invalid")
		}
	}
	return memoryAutomaticPreferences{enabled, policy}, nil
}

func (s *Server) memoryAutomaticPreferences(userID string) (memoryAutomaticPreferences, error) {
	if s == nil || s.settingsStore == nil {
		return memoryAutomaticPreferences{}, errors.New("automatic memory settings are unavailable")
	}
	setting, found, err := s.settingsStore.Get(userPreferenceSettingKey(memoryAutoExtractionSettingKey, strings.TrimSpace(userID)))
	if err != nil {
		return memoryAutomaticPreferences{}, fmt.Errorf("read automatic memory setting: %w", err)
	}
	return s.decodeMemoryAutomaticPreferences(setting.Value, found)
}

func (s *Server) updateMemoryAutomaticPreferences(userID string, enabled *bool, policy *memoryconfig.ForkedViewRecovery) (memoryAutomaticPreferences, error) {
	if s == nil || s.settingsStore == nil {
		return memoryAutomaticPreferences{}, errors.New("automatic memory settings are unavailable")
	}
	if policy != nil && !policy.Valid() {
		return memoryAutomaticPreferences{}, errors.New("automatic memory recovery policy is invalid")
	}
	s.memoryAutoExtractionCommitMu.Lock()
	defer s.memoryAutoExtractionCommitMu.Unlock()
	// Read/merge/write all fields inside the existing Store update mutex/rename.
	setting, err := s.settingsStore.Update(userPreferenceSettingKey(memoryAutoExtractionSettingKey, strings.TrimSpace(userID)), func(current any, found bool) (any, error) {
		preference, err := s.decodeMemoryAutomaticPreferences(current, found)
		if err != nil {
			return nil, err
		}
		if enabled != nil {
			preference.Enabled = *enabled
		}
		if policy != nil {
			preference.ForkedViewRecovery = *policy
		}
		return map[string]any{"enabled": preference.Enabled, "forked_view_recovery": string(preference.ForkedViewRecovery)}, nil
	})
	if err != nil {
		return memoryAutomaticPreferences{}, fmt.Errorf("persist automatic memory setting: %w", err)
	}
	return s.decodeMemoryAutomaticPreferences(setting.Value, true)
}

func automaticMemoryDecisionAllowed(preference memoryAutomaticPreferences, decision *workspace.MemoryExtractionModeDecision) bool {
	return !decision.Recovered() || preference.Enabled && preference.ForkedViewRecovery == memoryconfig.ForkedViewRecoveryCompact
}

func (s *Server) checkMemoryRecoveryPolicy(ctx context.Context, userID string, decision *workspace.MemoryExtractionModeDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !decision.Recovered() {
		return nil
	}
	preference, err := s.memoryAutomaticPreferences(userID)
	if err != nil || !automaticMemoryDecisionAllowed(preference, decision) {
		return fmt.Errorf("%w: automatic Memory recovery policy changed or is unavailable", workspace.ErrAgentMemoryMutationForbidden)
	}
	return nil
}
