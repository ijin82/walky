// Package config loads and validates walky configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Mode is the sync direction model.
type Mode string

const (
	// ModeTwoWay is a full bidirectional sync with three-way merge.
	ModeTwoWay Mode = "two-way"
	// ModePushOnly only uploads local changes; the drive is history only.
	ModePushOnly Mode = "push-only"
)

// ConflictPolicy decides what happens when both sides changed a path.
type ConflictPolicy string

const (
	// ConflictCopy keeps both variants (the local one is renamed).
	ConflictCopy ConflictPolicy = "copy"
	// ConflictNewest keeps the variant with the newer mtime.
	ConflictNewest ConflictPolicy = "newest"
	// ConflictLocalWins keeps the local variant.
	ConflictLocalWins ConflictPolicy = "local-wins"
)

// Config is the root of ~/.config/walky/config.toml.
type Config struct {
	Repo      Repo      `toml:"repo"`
	Sync      Sync      `toml:"sync"`
	Exclude   Exclude   `toml:"exclude"`
	Retention Retention `toml:"retention"`
	Device    Device    `toml:"device"`

	// Path is where the config was loaded from (filled by Load).
	Path string `toml:"-"`
}

// Repo describes the local sync root and the drive identity we trust.
type Repo struct {
	Path   string `toml:"path"`
	RepoID string `toml:"repo_id"`
}

// Sync tunes the sync engine.
type Sync struct {
	Mode          Mode           `toml:"mode"`
	Conflict      ConflictPolicy `toml:"conflict"`
	DebounceMS    int            `toml:"debounce_ms"`
	FullScanMin   int            `toml:"full_scan_min"`
	Workers       int            `toml:"workers"`
	MaxFileSize   string         `toml:"max_file_size"`
	SyncOnBattery bool           `toml:"sync_on_battery"`
}

// Exclude lists paths that are never synced.
type Exclude struct {
	Patterns []string `toml:"patterns"`
}

// Retention controls how much history is kept on the drive.
type Retention struct {
	Daily     int `toml:"daily"`
	Weekly    int `toml:"weekly"`
	Monthly   int `toml:"monthly"`
	TrashDays int `toml:"trash_days"`
}

// Device tunes drive detection.
type Device struct {
	AutoSync       bool `toml:"auto_sync"`
	EjectAfterSync bool `toml:"eject_after_sync"`
}

// DefaultPath returns the default config location.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "walky", "config.toml"), nil
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Repo: Repo{Path: "~/Sync"},
		Sync: Sync{
			Mode:        ModeTwoWay,
			Conflict:    ConflictCopy,
			DebounceMS:  3000,
			FullScanMin: 10,
			Workers:     0, // auto
			MaxFileSize: "8G",
		},
		Exclude: Exclude{Patterns: []string{
			"**/node_modules",
			"**/target",
			"**/build",
			"**/.venv",
			"**/__pycache__",
			"**/.cache",
			"**/*.lock",
			"**/*.tmp",
		}},
		Retention: Retention{Daily: 14, Weekly: 8, Monthly: 6, TrashDays: 30},
		Device:    Device{AutoSync: true},
	}
}

// Load reads the config at path (or the default path when empty). A missing
// file is not an error: defaults are returned, so the CLI works before the
// user creates a config.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return cfg, err
		}
		path = p
	}
	cfg.Path = path

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks invariants that would otherwise fail deep inside the daemon.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Repo.Path) == "" {
		return errors.New("repo.path must not be empty")
	}
	switch c.Sync.Mode {
	case ModeTwoWay, ModePushOnly:
	default:
		return fmt.Errorf("sync.mode: unknown value %q", c.Sync.Mode)
	}
	switch c.Sync.Conflict {
	case ConflictCopy, ConflictNewest, ConflictLocalWins:
	default:
		return fmt.Errorf("sync.conflict: unknown value %q", c.Sync.Conflict)
	}
	if c.Sync.DebounceMS < 0 || c.Sync.FullScanMin < 0 {
		return errors.New("sync.debounce_ms and sync.full_scan_min must be >= 0")
	}
	if c.Sync.Workers < 0 {
		return errors.New("sync.workers must be >= 0")
	}
	if c.Retention.Daily < 0 || c.Retention.Weekly < 0 || c.Retention.Monthly < 0 {
		return errors.New("retention counts must be >= 0")
	}
	if c.Retention.TrashDays < 0 {
		return errors.New("retention.trash_days must be >= 0")
	}
	return nil
}

// MaxFileSizeBytes parses sync.max_file_size. Empty means no limit.
func (s Sync) MaxFileSizeBytes() (int64, error) {
	v := strings.TrimSpace(strings.ToUpper(s.MaxFileSize))
	if v == "" || v == "0" {
		return 0, nil
	}
	mult := int64(1)
	switch v[len(v)-1] {
	case 'K':
		mult = 1 << 10
		v = v[:len(v)-1]
	case 'M':
		mult = 1 << 20
		v = v[:len(v)-1]
	case 'G':
		mult = 1 << 30
		v = v[:len(v)-1]
	case 'T':
		mult = 1 << 40
		v = v[:len(v)-1]
	case 'B':
		v = v[:len(v)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid max_file_size %q", s.MaxFileSize)
	}
	return n * mult, nil
}

// ExpandedRepoPath returns repo.path with a leading ~ expanded.
func (c Config) ExpandedRepoPath() (string, error) {
	p := c.Repo.Path
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

// Save writes the configuration as TOML, replacing the file atomically.
// Comments in the previous file are not preserved.
func Save(path string, cfg Config) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}
