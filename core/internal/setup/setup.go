// Package setup initializes stores and binds them to a local repository. It is
// shared by the CLI and the daemon (and therefore by the GUI over D-Bus).
package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"walky/internal/config"
	"walky/internal/store"
)

// Result describes a freshly initialized store.
type Result struct {
	StoreRoot string
	RepoID    string
	Format    int
}

// Initialize validates the filesystem, creates the store layout and returns
// its marker. It never formats a device and only creates the store directory.
func Initialize(storeRoot string, force bool, check func(string) error) (Result, error) {
	if check == nil {
		check = store.CheckFilesystem
	}
	if err := check(storeRoot); err != nil {
		return Result{}, err
	}
	st, err := store.Init(storeRoot, force)
	if err != nil {
		return Result{}, err
	}
	marker := st.Marker()
	return Result{StoreRoot: st.Root(), RepoID: marker.RepoID, Format: marker.Format}, nil
}

// ConfigureRepo points the configuration at an existing directory and pins the
// drive identity. The directory must already exist.
func ConfigureRepo(configPath, repoPath, repoID string) error {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("create sync folder: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("sync folder: %w", err)
	}
	if !fi.IsDir() {
		return errors.New("sync folder must be a directory")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.Repo.Path = abs
	if repoID != "" {
		cfg.Repo.RepoID = repoID
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return config.Save(configPath, cfg)
}
