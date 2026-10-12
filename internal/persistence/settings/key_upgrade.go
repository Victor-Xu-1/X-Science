package settings

import (
	"errors"
	"reflect"
	"sort"
)

// RenameKeys transfers an explicit caller-owned key set in one store commit.
// Values and update timestamps remain unchanged. Conflicting targets abort the
// entire upgrade before any write; this does not guess product-specific keys.
func (s *Store) RenameKeys(replacements map[string]string) error {
	keys := make([]string, 0, len(replacements))
	targets := make(map[string]bool, len(replacements))
	for source, target := range replacements {
		if err := validateKey(source); err != nil {
			return err
		}
		if err := validateKey(target); err != nil {
			return err
		}
		if source == target || targets[target] {
			return errors.New("settings upgrade targets must be distinct")
		}
		targets[target] = true
		keys = append(keys, source)
	}
	for source := range replacements {
		if targets[source] {
			return errors.New("settings upgrade must not contain chains or cycles")
		}
	}
	sort.Strings(keys)
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return err
	}
	changed := false
	for _, source := range keys {
		value, found := data.Settings[source]
		if !found {
			continue
		}
		target := replacements[source]
		if current, found := data.Settings[target]; found {
			if !reflect.DeepEqual(current.Value, value.Value) {
				return errors.New("settings namespace upgrade has conflicting existing values")
			}
		} else {
			value.Key = target
			data.Settings[target] = value
		}
		delete(data.Settings, source)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveLocked(data)
}
