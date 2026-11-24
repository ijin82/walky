package daemon

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"walky/internal/config"
)

func settingsDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	repo := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Default()
	cfg.Repo.Path = repo
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	d := New(Config{
		RepoPath:   repo,
		ConfigPath: cfgPath,
		Excludes:   cfg.Exclude.Patterns,
		Conflict:   string(cfg.Sync.Conflict),
		Retention: RetentionSettings{
			Daily: cfg.Retention.Daily, Weekly: cfg.Retention.Weekly,
			Monthly: cfg.Retention.Monthly, TrashDays: cfg.Retention.TrashDays,
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return d, cfgPath
}

func TestUpdateSettingsPersists(t *testing.T) {
	d, cfgPath := settingsDaemon(t)
	newSettings := Settings{
		Excludes:  []string{"**/node_modules", "**/tmp"},
		Retention: RetentionSettings{Daily: 3, Weekly: 2, Monthly: 1, TrashDays: 5},
		Conflict:  string(config.ConflictNewest),
	}
	if err := d.UpdateSettings(newSettings); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exclude.Patterns) != 2 || loaded.Exclude.Patterns[1] != "**/tmp" {
		t.Fatalf("excludes = %v", loaded.Exclude.Patterns)
	}
	if loaded.Retention.Daily != 3 || loaded.Retention.TrashDays != 5 {
		t.Fatalf("retention = %+v", loaded.Retention)
	}
	if loaded.Sync.Conflict != config.ConflictNewest {
		t.Fatalf("conflict = %q", loaded.Sync.Conflict)
	}
	if got := d.Settings(); got.Conflict != string(config.ConflictNewest) || got.Retention.Daily != 3 {
		t.Fatalf("runtime settings = %+v", got)
	}
}

func TestReloadConfig(t *testing.T) {
	d, cfgPath := settingsDaemon(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Exclude.Patterns = []string{"**/build"}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := d.ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	got := d.Settings()
	if len(got.Excludes) != 1 || got.Excludes[0] != "**/build" {
		t.Fatalf("excludes after reload = %v", got.Excludes)
	}
}

func TestSettingsRejectInvalidConflict(t *testing.T) {
	d, _ := settingsDaemon(t)
	err := d.UpdateSettings(Settings{
		Retention: RetentionSettings{Daily: 1},
		Conflict:  "coin-flip",
	})
	if err == nil {
		t.Fatal("invalid conflict policy was accepted")
	}
}

func TestSettingsRejectRepoChange(t *testing.T) {
	d, cfgPath := settingsDaemon(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repo.Path = t.TempDir()
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := d.ReloadConfig(); err == nil {
		t.Fatal("repo change was accepted without a restart")
	}
}
