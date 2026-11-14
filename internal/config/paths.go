// Package config holds mongo-tui settings and local state.
//
// Nothing in here ever stores a password: MongoDB credentials stay in the TUI
// prompt.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Paths are the XDG locations mongo-tui reads and writes. Legacy* point at the
// psql-tui files copied on the first start after the rename.
type Paths struct {
	ConfigDir        string
	ConfigFile       string
	StateFile        string
	LegacyConfigFile string
	LegacyStateFile  string
}

// DefaultPaths resolves the XDG locations. It creates nothing; missing
// directories are created on first write.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("cannot determine the home directory: %w", err)
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	configDir := filepath.Join(configHome, "mongo-tui")

	return Paths{
		ConfigDir:        configDir,
		ConfigFile:       filepath.Join(configDir, "config.toml"),
		StateFile:        filepath.Join(stateHome, "mongo-tui", "state.json"),
		LegacyConfigFile: filepath.Join(configHome, "psql-tui", "config.toml"),
		LegacyStateFile:  filepath.Join(stateHome, "psql-tui", "state.json"),
	}, nil
}

// MigrateLegacy copies psql-tui config/state into the mongo-tui locations on
// first start. New files are never overwritten, the old files are left in
// place, and a .psql-tui.bak copy is written next to each migrated file.
func MigrateLegacy(paths Paths) error {
	if err := migrateFile(paths.LegacyConfigFile, paths.ConfigFile); err != nil {
		return fmt.Errorf("config migration: %w", err)
	}
	if err := migrateFile(paths.LegacyStateFile, paths.StateFile); err != nil {
		return fmt.Errorf("state migration: %w", err)
	}
	return nil
}

func migrateFile(legacy, target string) error {
	if legacy == "" || target == "" {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		return nil // the new file already exists; never overwrite it
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := EnsurePrivateDir(filepath.Dir(target)); err != nil {
		return err
	}
	backup := target + ".psql-tui.bak"
	if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(backup, data, 0o600); err != nil {
			return fmt.Errorf("backup %s: %w", backup, err)
		}
	}
	return os.WriteFile(target, data, 0o600)
}

// EnsurePrivateDir creates dir and its parents (mode 0700) and refuses a
// directory that other users can reach.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is accessible to other users (mode %04o), expected 0700; run: chmod 700 %s", dir, perm, dir)
	}
	return nil
}
