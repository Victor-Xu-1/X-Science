package server

import (
	"encoding/json"
	"errors"
	"sort"
)

var errSkillImportMetadata = errors.New("skill import metadata could not be persisted")

// Source provenance and import history remain in the existing settings file;
// a batch is one atomic save, not a second metadata store or SQLite callback.
func (s *Server) recordSkillImports(userID string, source *webSkillMarketplaceSource, entries []webSkillImportHistoryEntry) error {
	if s.settingsStore == nil {
		return errors.Join(errSkillImportMetadata, errors.New("settings store is not configured"))
	}
	updates := make(map[string]func(any, bool) (any, error), 2)
	if source != nil {
		updates[webSkillUserSettingKey(webSkillMarketplaceSourcesSetting, userID)] = func(current any, found bool) (any, error) {
			sources := []webSkillMarketplaceSource{}
			if found {
				if err := decodeSkillImportSetting(current, &sources); err != nil {
					return nil, err
				}
			}
			next := make([]webSkillMarketplaceSource, 0, len(sources))
			for _, existing := range sources {
				if existing.Slug != source.Slug {
					next = append(next, existing)
				}
			}
			next = append(next, *source)
			sort.Slice(next, func(i, j int) bool { return next[i].Slug < next[j].Slug })
			return next, nil
		}
	}
	if len(entries) > 0 {
		updates[webSkillUserSettingKey(webSkillImportHistorySetting, userID)] = func(current any, found bool) (any, error) {
			history := []webSkillImportHistoryEntry{}
			if found {
				if err := decodeSkillImportSetting(current, &history); err != nil {
					return nil, err
				}
			}
			history = append(append([]webSkillImportHistoryEntry(nil), entries...), history...)
			if len(history) > maxWebSkillImportHistory {
				history = history[:maxWebSkillImportHistory]
			}
			return history, nil
		}
	}
	if err := s.settingsStore.UpdateBatch(updates); err != nil {
		return errors.Join(errSkillImportMetadata, err)
	}
	return nil
}

func decodeSkillImportSetting(value any, output any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}
