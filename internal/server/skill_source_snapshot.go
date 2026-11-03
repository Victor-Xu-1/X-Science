package server

import (
	"context"
	"encoding/json"
	"errors"

	settingstore "x-science/internal/persistence/settings"
	"x-science/internal/skills"
)

// A request owns one exact pair of settings keys and releases their revision
// metadata on every exit. Values still come only from the original Store.
type webSkillSourceSnapshot struct {
	owner    string
	pathKey  string
	settings *settingstore.Snapshot
	paths    []webSkillExternalPath
	grants   []hostGrant
	grantErr error
	catalog  skills.CatalogSnapshot
}

var errWebSkillSourceMutationBusy = errors.New("Skill publication is busy; retry source removal when it finishes")

// Explicit user removal owns the existing publisher guard through the settings
// write and projection cleanup, as other explicit publishers do. It never
// acquires hostGrantKernelMu. Read-side source admission remains memory-only.
func (s *Server) deleteExternalWebSkillSource(ctx context.Context, owner, canonical string) (removed, changed bool, err error) {
	if ctx == nil {
		return false, false, skills.ErrNilLoadContext
	}
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if !s.skillMutationMu.TryLock() {
		return false, false, errWebSkillSourceMutationBusy
	}
	defer s.skillMutationMu.Unlock()
	source, err := s.captureWebSkillSourceSnapshot(ctx, owner)
	if err != nil {
		return false, false, err
	}
	defer source.close()
	present := false
	for _, item := range source.paths {
		present = present || sameCleanPath(item.Path, canonical)
	}
	if !present {
		// An absent owner registration cannot authorize removing another
		// source's projection merely because its path was supplied by a caller.
		return false, false, nil
	}
	updated, err := s.updateWebSkillSourceSnapshot(ctx, source, func(paths []webSkillExternalPath) ([]webSkillExternalPath, error) {
		next := make([]webSkillExternalPath, 0, len(paths))
		for _, item := range paths {
			if sameCleanPath(item.Path, canonical) {
				removed = true
				continue
			}
			next = append(next, item)
		}
		return next, nil
	})
	if err != nil {
		return false, false, err
	}
	defer updated.close()
	// The accepted settings mutation must finish its cleanup even if the
	// request is now canceled or a later POST has saved a fresh registration.
	// That POST cannot publish its content until this same guard is released.
	if removed {
		changed = len(s.skillCatalog.RemoveSkillsUnder(canonical)) > 0
	}
	return removed, changed, nil
}

func (snapshot *webSkillSourceSnapshot) close() {
	if snapshot != nil {
		snapshot.settings.Close()
	}
}

func (s *Server) captureWebSkillSourceSnapshot(ctx context.Context, owner string) (*webSkillSourceSnapshot, error) {
	if s == nil || s.settingsStore == nil || s.skillCatalog == nil {
		return nil, errors.New("Skill source authority is unavailable")
	}
	pathKey := webSkillUserSettingKey(webSkillExternalPathsSetting, owner)
	observed, err := s.settingsStore.SnapshotKeys(ctx, pathKey, hostGrantsSettingKeyForUser(owner))
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeWebSkillSourceSnapshot(owner, pathKey, observed)
	if err != nil {
		observed.Close()
		return nil, err
	}
	snapshot.catalog = s.skillCatalog.Snapshot()
	return snapshot, nil
}

func decodeWebSkillSourceSnapshot(owner, pathKey string, observed *settingstore.Snapshot) (*webSkillSourceSnapshot, error) {
	snapshot := &webSkillSourceSnapshot{owner: owner, pathKey: pathKey, settings: observed}
	paths, found, err := observed.Lookup(pathKey)
	if err != nil {
		return nil, err
	}
	if found {
		if snapshot.paths, err = decodeWebSkillExternalPaths(paths.Value); err != nil {
			return nil, err
		}
	}
	grants, found, err := observed.Lookup(hostGrantsSettingKeyForUser(owner))
	if err != nil {
		return nil, err
	}
	if found {
		snapshot.grants, snapshot.grantErr = decodeHostGrants(grants.Value)
	}
	return snapshot, nil
}

func decodeWebSkillExternalPaths(value any) ([]webSkillExternalPath, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	paths := []webSkillExternalPath{}
	if err := json.Unmarshal(data, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Server) updateWebSkillSourceSnapshot(ctx context.Context, previous *webSkillSourceSnapshot, update func([]webSkillExternalPath) ([]webSkillExternalPath, error)) (*webSkillSourceSnapshot, error) {
	observed, err := s.settingsStore.CompareUpdate(ctx, previous.settings, previous.pathKey, func(value any, found bool) (any, error) {
		paths := []webSkillExternalPath{}
		var err error
		if found {
			if paths, err = decodeWebSkillExternalPaths(value); err != nil {
				return nil, err
			}
		}
		return update(paths)
	})
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeWebSkillSourceSnapshot(previous.owner, previous.pathKey, observed)
	if err != nil {
		observed.Close()
		return nil, err
	}
	snapshot.catalog = previous.catalog
	return snapshot, nil
}

func (s *Server) compareApplyExternalWebSkills(ctx context.Context, snapshot skills.CatalogSnapshot, sources [][]skills.Skill, authority *webSkillSourceSnapshot) (bool, error) {
	if ctx == nil {
		return false, skills.ErrNilLoadContext
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if authority == nil || authority.settings == nil {
		return false, errors.New("Skill source snapshot is required")
	}
	if authority.grantErr != nil {
		return false, nil
	}
	upserts := externalWebSkillUpserts(snapshot.Skills, sources)
	// All file/settings IO and planning are complete. Keep the existing lock
	// order, skip busy owners, and never perform IO/DB work in this final action.
	if !s.hostGrantKernelMu.TryLock() {
		return false, ctx.Err()
	}
	defer s.hostGrantKernelMu.Unlock()
	if s.hostGrantKernelFences[authority.owner] {
		return false, nil
	}
	if !s.skillMutationMu.TryLock() {
		return false, ctx.Err()
	}
	defer s.skillMutationMu.Unlock()
	changed := false
	err := s.settingsStore.TryCompare(ctx, authority.settings, func() error {
		var err error
		changed, err = s.skillCatalog.CompareApply(ctx, snapshot.Revision, upserts)
		return err
	})
	if errors.Is(err, settingstore.ErrSnapshotBusy) || errors.Is(err, settingstore.ErrSnapshotConflict) || errors.Is(err, skills.ErrCatalogRevisionConflict) {
		return false, nil
	}
	return changed, err
}
