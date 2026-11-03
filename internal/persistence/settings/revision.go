package settings

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrSnapshotConflict = errors.New("settings changed after their snapshot")
	ErrSnapshotBusy     = errors.New("settings mutation is in progress")
)

// Slots carry only revision metadata for keys with live snapshots, never a
// second settings/permission cache. Close releases their references; the last
// snapshot deletes each slot, including tombstones for deleted keys.
type revisionIdentity struct{ marker byte }
type revisionSlot struct {
	identity *revisionIdentity
	readers  int
}
type snapshotEntry struct {
	setting  Setting
	found    bool
	slot     *revisionSlot
	identity *revisionIdentity
}

type Snapshot struct {
	store   *Store
	entries map[string]snapshotEntry
	closed  bool // protected by the owning Store.revisionMu
}

func (s *Store) SnapshotKeys(ctx context.Context, keys ...string) (*Snapshot, error) {
	if err := snapshotContextError(ctx); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("settings snapshot keys are required")
	}
	unique := make(map[string]bool, len(keys))
	for _, key := range keys {
		if err := validateKey(key); err != nil {
			return nil, err
		}
		unique[key] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.snapshotLocked(data, unique)
}

// Lookup returns an owned JSON value. A caller cannot modify the data retained
// by the snapshot or manufacture a Store-bound revision by changing this value.
func (snapshot *Snapshot) Lookup(key string) (Setting, bool, error) {
	if snapshot == nil {
		return Setting{}, false, ErrSnapshotConflict
	}
	entry, exists := snapshot.entries[key]
	if !exists || !entry.found {
		return Setting{}, false, nil
	}
	setting := entry.setting
	value, err := ownedSnapshotValue(setting.Value)
	if err != nil {
		return Setting{}, false, err
	}
	setting.Value = value
	return setting, true, nil
}

func (snapshot *Snapshot) Close() {
	if snapshot == nil || snapshot.store == nil {
		return
	}
	s := snapshot.store
	// Releasing request metadata must not wait for settings file IO after a
	// busy final commit. This lock guards only short in-memory token operations.
	s.revisionMu.Lock()
	defer s.revisionMu.Unlock()
	if snapshot.closed {
		return
	}
	snapshot.closed = true
	for key, entry := range snapshot.entries {
		entry.slot.readers--
		if entry.slot.readers == 0 {
			delete(s.revisions, key)
		}
	}
	if len(s.revisions) == 0 {
		s.revisions = nil
	}
}

// TryCompare holds the original settings mutex through a memory-only commit.
// The callback must not do IO, reenter Store, or acquire another Server lock.
func (s *Store) TryCompare(ctx context.Context, expected *Snapshot, commit func() error) error {
	if err := snapshotContextError(ctx); err != nil {
		return err
	}
	if commit == nil {
		return errors.New("settings snapshot commit is required")
	}
	if !s.mu.TryLock() {
		return ErrSnapshotBusy
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.matchesSnapshotLocked(expected) {
		return ErrSnapshotConflict
	}
	return commit()
}

// CompareUpdate reads/writes only under Store.mu, without Server publication or
// grant locks. A pure transform updates one observed key and returns a fresh
// snapshot for subsequent content admission; the original snapshot stays stale.
func (s *Store) CompareUpdate(ctx context.Context, expected *Snapshot, key string, update func(any, bool) (any, error)) (*Snapshot, error) {
	if err := snapshotContextError(ctx); err != nil {
		return nil, err
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if update == nil {
		return nil, errors.New("settings snapshot update is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesSnapshotLocked(expected) {
		return nil, ErrSnapshotConflict
	}
	if _, found := expected.entries[key]; !found {
		return nil, ErrSnapshotConflict
	}
	data, err := s.loadLocked()
	if err != nil {
		s.invalidateRevisionsLocked(key)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, found := data.Settings[key]
	value, err := update(current.Value, found)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data.Settings[key] = Setting{Key: key, Value: value, UpdatedAt: s.now().UTC()}
	// Invalidate before the save attempt, including failed/uncertain writes.
	s.invalidateRevisionsLocked(key)
	if err := s.saveLocked(data); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(expected.entries))
	for observed := range expected.entries {
		keys[observed] = true
	}
	return s.snapshotLocked(data, keys)
}

func (s *Store) snapshotLocked(data fileData, keys map[string]bool) (*Snapshot, error) {
	snapshot := &Snapshot{store: s, entries: make(map[string]snapshotEntry, len(keys))}
	for key := range keys {
		setting, found := data.Settings[key]
		if found {
			value, err := ownedSnapshotValue(setting.Value)
			if err != nil {
				return nil, err
			}
			setting.Value = value
		}
		snapshot.entries[key] = snapshotEntry{setting: setting, found: found}
	}
	s.revisionMu.Lock()
	defer s.revisionMu.Unlock()
	if s.revisions == nil {
		s.revisions = make(map[string]*revisionSlot)
	}
	for key := range keys {
		slot := s.revisions[key]
		if slot == nil {
			slot = &revisionSlot{identity: &revisionIdentity{}}
			s.revisions[key] = slot
		}
		slot.readers++
		entry := snapshot.entries[key]
		entry.slot, entry.identity = slot, slot.identity
		snapshot.entries[key] = entry
	}
	return snapshot, nil
}

func ownedSnapshotValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var owned any
	err = json.Unmarshal(data, &owned)
	return owned, err
}

func (s *Store) matchesSnapshotLocked(snapshot *Snapshot) bool {
	s.revisionMu.Lock()
	defer s.revisionMu.Unlock()
	if snapshot == nil || snapshot.store != s || snapshot.closed {
		return false
	}
	for key, entry := range snapshot.entries {
		if s.revisions[key] != entry.slot || entry.slot.identity != entry.identity {
			return false
		}
	}
	return true
}

func (s *Store) invalidateRevisionsLocked(keys ...string) {
	s.revisionMu.Lock()
	defer s.revisionMu.Unlock()
	for _, key := range keys {
		if slot := s.revisions[key]; slot != nil {
			slot.identity = &revisionIdentity{}
		}
	}
}

func snapshotContextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("settings snapshot context is required")
	}
	return ctx.Err()
}
