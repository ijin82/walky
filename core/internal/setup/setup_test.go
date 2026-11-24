package setup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"walky/internal/config"
)

func TestInitializeAndConfigureRepo(t *testing.T) {
	dir := t.TempDir()
	storeRoot := filepath.Join(dir, "WALKY")

	// Filesystem check failure must abort before creating the store.
	wantErr := errors.New("unsupported fs")
	if _, err := Initialize(storeRoot, false, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("Initialize err = %v, want %v", err, wantErr)
	}

	res, err := Initialize(storeRoot, false, func(string) error { return nil })
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.RepoID == "" || res.Format <= 0 || res.StoreRoot != storeRoot {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Repeat init without force must fail.
	if _, err := Initialize(storeRoot, false, func(string) error { return nil }); err == nil {
		t.Fatal("repeat Initialize succeeded without force")
	}

	// ConfigureRepo creates the sync folder if missing and persists config.
	cfgPath := filepath.Join(dir, "config.toml")
	repoPath := filepath.Join(dir, "Sync")
	if err := ConfigureRepo(cfgPath, repoPath, res.RepoID); err != nil {
		t.Fatalf("ConfigureRepo: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Repo.Path != repoPath || cfg.Repo.RepoID != res.RepoID {
		t.Fatalf("saved config = %+v", cfg.Repo)
	}

	// Pointing ConfigureRepo at a regular file must fail.
	filePath := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureRepo(cfgPath, filePath, res.RepoID); err == nil {
		t.Fatal("ConfigureRepo accepted a regular file")
	}
}
