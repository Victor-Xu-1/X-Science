package datadir

import (
	"errors"
	"path/filepath"
	"strings"
)

// UpgradeLegacyDefaultHomePointer rewrites a persisted default data-directory
// pointer that still references the original product default home directory.
// Custom operator paths are left unchanged.
func UpgradeLegacyDefaultHomePointer(controlPath, legacyHome, canonicalHome string) error {
	if !filepath.IsAbs(controlPath) || !filepath.IsAbs(legacyHome) || !filepath.IsAbs(canonicalHome) {
		return errors.New("default home pointer upgrade requires absolute paths")
	}
	controller := New(controlPath)
	state, err := controller.Load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.Current) == "" {
		return nil
	}
	current, err := canonicalExistingDirectory(state.Current)
	if err != nil {
		return nil
	}
	legacy, err := canonicalExistingDirectory(legacyHome)
	if err != nil {
		return nil
	}
	canonical, err := canonicalOrCreateDirectory(canonicalHome)
	if err != nil {
		return err
	}
	if current != legacy || current == canonical {
		return nil
	}
	return controller.rewriteCurrentDirectory(canonical)
}

func (c *Controller) rewriteCurrentDirectory(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.loadLocked()
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.Current) == "" {
		return nil
	}
	current, err := canonicalExistingDirectory(state.Current)
	if err != nil {
		return err
	}
	if current == target {
		return nil
	}
	state.Current = target
	return c.saveLocked(state)
}
