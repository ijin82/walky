package daemon

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"walky/internal/config"
	"walky/internal/device"
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
		Excludes:     []string{"**/node_modules", "**/tmp"},
		Retention:    RetentionSettings{Daily: 3, Weekly: 2, Monthly: 1, TrashDays: 5},
		Conflict:     string(config.ConflictNewest),
		PollSeconds:  45,
		NotifyOnSync: true,
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
	if loaded.Sync.PollSeconds != 45 {
		t.Fatalf("poll_seconds = %d", loaded.Sync.PollSeconds)
	}
	if !loaded.Sync.NotifyOnSync {
		t.Fatal("notify_on_sync was not persisted")
	}
	if got := d.Settings(); got.Conflict != string(config.ConflictNewest) || got.Retention.Daily != 3 || !got.NotifyOnSync {
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

func TestRepoChangeAllowedWhenIdle(t *testing.T) {
	d, cfgPath := settingsDaemon(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	newRepo := t.TempDir()
	cfg.Repo.Path = newRepo
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := d.ReloadConfig(); err != nil {
		t.Fatalf("reload while idle: %v", err)
	}
	if d.cfg.RepoPath != newRepo {
		t.Fatalf("repo = %q, want %q", d.cfg.RepoPath, newRepo)
	}

	// While a drive is attached the repository cannot move under a sync.
	third := t.TempDir()
	cfg.Repo.Path = third
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	d.dev = &device.Device{StoreRoot: newRepo, MountPoint: newRepo}
	if err := d.ReloadConfig(); err == nil {
		t.Fatal("repo change was accepted while a drive is attached")
	}
}

func TestSetRepoPath(t *testing.T) {
	d, cfgPath := settingsDaemon(t)
	nextRepo := t.TempDir()

	if err := d.SetRepoPath(nextRepo); err != nil {
		t.Fatalf("SetRepoPath: %v", err)
	}
	if got := d.Status().RepoPath; got != nextRepo {
		t.Fatalf("Status.RepoPath = %q, want %q", got, nextRepo)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repo.Path != nextRepo {
		t.Fatalf("config repo.path = %q, want %q", cfg.Repo.Path, nextRepo)
	}

	// Overlapping the attached store must be rejected.
	storeDir := t.TempDir()
	d.dev = &device.Device{StoreRoot: storeDir, MountPoint: storeDir}
	if err := d.SetRepoPath(storeDir); err == nil {
		t.Fatal("SetRepoPath accepted the store directory itself")
	}

	// Changing while syncing must be rejected.
	d.syncing = true
	if err := d.SetRepoPath(t.TempDir()); err == nil {
		t.Fatal("SetRepoPath accepted change while syncing")
	}
}

func TestEventsPersistence(t *testing.T) {
	eventsPath := filepath.Join(t.TempDir(), "events.json")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d1 := New(Config{RepoPath: t.TempDir(), EventsPath: eventsPath, NoTray: true}, log)

	for i := 1; i <= 60; i++ {
		d1.recordEvent(Event{Path: filepath.Join("dir", string(rune('a'+(i%26)))), Op: "push", Result: "ok", Time: int64(i)})
	}
	// Summary event flushes the last 50 events to disk.
	d1.recordEvent(Event{Path: "/store", Op: "sync", Result: "ok", Time: 61})

	d2 := New(Config{RepoPath: t.TempDir(), EventsPath: eventsPath, NoTray: true}, log)
	got := d2.Events(100)
	if len(got) != maxPersistedEvents {
		t.Fatalf("persisted events = %d, want %d", len(got), maxPersistedEvents)
	}
	// Newest first (Time 61 down to 12).
	if got[0].Time != 61 || got[0].Op != "sync" {
		t.Fatalf("newest event = %+v, want sync at t=61", got[0])
	}
	if got[len(got)-1].Time != 12 {
		t.Fatalf("oldest persisted event = %+v, want t=12", got[len(got)-1])
	}
}
