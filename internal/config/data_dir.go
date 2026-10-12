package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	productidentity "x-science"
	"x-science/internal/datadir"
)

func applyDataDirectoryControl(cfg *Config, configPath string) error {
	cfg.DefaultDataDir = defaultHome()
	controlPath, err := dataDirectoryControlPath()
	if err != nil {
		return err
	}
	cfg.DataDirControlPath = controlPath
	controller := datadir.New(controlPath)
	state, err := controller.Load()
	if err != nil {
		return fmt.Errorf("load data directory control: %w", err)
	}
	// Environment and config values provide the initial location. Once the
	// user explicitly chooses a location in settings, the durable pointer is
	// authoritative so a restart can honor that choice even when the process
	// was originally launched with X_SCIENCE_HOME or a config override.
	if state.Current != "" || state.Pending != nil {
		resolved, _, err := controller.Resolve(cfg.DefaultDataDir)
		if err != nil {
			return fmt.Errorf("resolve data directory pointer: %w", err)
		}
		cfg.HomeDir = resolved
		cfg.DataDirSource = "pointer"
		return nil
	}
	if strings.TrimSpace(os.Getenv("X_SCIENCE_HOME")) != "" {
		cfg.DataDirSource = "env"
		return nil
	}
	if strings.TrimSpace(configPath) != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if homeRaw, ok := fields["home_dir"]; ok {
			var home string
			if json.Unmarshal(homeRaw, &home) == nil && strings.TrimSpace(home) != "" {
				cfg.DataDirSource = "config"
				return nil
			}
		}
	}
	resolved, source, err := controller.Resolve(defaultHome())
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	cfg.HomeDir = resolved
	cfg.DataDirSource = source
	return nil
}

func dataDirectoryControlPath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("X_SCIENCE_DATA_DIR_CONTROL")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("X_SCIENCE_DATA_DIR_CONTROL must be an absolute path")
		}
		return filepath.Clean(configured), nil
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	if !filepath.IsAbs(configDirectory) {
		absolute, err := filepath.Abs(configDirectory)
		if err != nil {
			return "", err
		}
		configDirectory = absolute
	}
	current := filepath.Join(configDirectory, productidentity.Current().MachineSlug, "data-dir.json")
	previous := filepath.Join(configDirectory, "synon-go", "data-dir.json") // Original default pointer, consumed only by the upgrade.
	if err := datadir.UpgradeDefaultPointer(previous, current); err != nil {
		return "", fmt.Errorf("upgrade default data directory pointer: %w", err)
	}
	return current, nil
}
