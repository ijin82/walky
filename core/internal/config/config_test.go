package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if cfg.Sync.Mode != ModeTwoWay || cfg.Sync.Conflict != ConflictCopy {
		t.Fatalf("unexpected defaults: %+v", cfg.Sync)
	}
}

func TestLoadMissingUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path != p {
		t.Fatalf("Path = %q, want %q", cfg.Path, p)
	}
	if cfg.Sync.Conflict != ConflictCopy {
		t.Fatalf("missing file should fall back to defaults, got %+v", cfg.Sync)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	data := `[repo]
path = "/tmp/sync"
repo_id = "abc"

[sync]
mode = "push-only"
conflict = "newest"
debounce_ms = 500

[retention]
daily = 3
`
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Repo.Path != "/tmp/sync" || cfg.Repo.RepoID != "abc" {
		t.Fatalf("repo not loaded: %+v", cfg.Repo)
	}
	if cfg.Sync.Mode != ModePushOnly || cfg.Sync.Conflict != ConflictNewest {
		t.Fatalf("sync not loaded: %+v", cfg.Sync)
	}
	if cfg.Sync.DebounceMS != 500 || cfg.Retention.Daily != 3 {
		t.Fatalf("values not loaded: %+v %+v", cfg.Sync, cfg.Retention)
	}
	// Untouched keys keep defaults.
	if cfg.Retention.Weekly != 8 {
		t.Fatalf("weekly = %d, want default 8", cfg.Retention.Weekly)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := map[string]func(*Config){
		"empty path":   func(c *Config) { c.Repo.Path = " " },
		"bad mode":     func(c *Config) { c.Sync.Mode = "sometimes" },
		"bad conflict": func(c *Config) { c.Sync.Conflict = "coin-flip" },
		"negative":     func(c *Config) { c.Sync.DebounceMS = -1 },
		"bad workers":  func(c *Config) { c.Sync.Workers = -2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestExpandedRepoPath(t *testing.T) {
	cfg := Default()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got, err := cfg.ExpandedRepoPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Sync"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
