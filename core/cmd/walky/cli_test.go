package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"walky/internal/store"
)

func allowAnyFS(t *testing.T) {
	t.Helper()
	old := filesystemCheck
	filesystemCheck = func(string) error { return nil }
	t.Cleanup(func() { filesystemCheck = old })
}

func cliEnv(t *testing.T) (storeDir, repoDir string) {
	t.Helper()
	allowAnyFS(t)
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	storeDir = filepath.Join(home, "store")
	repoDir = filepath.Join(home, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return storeDir, repoDir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIStorageRoundTrip(t *testing.T) {
	storeDir, repoDir := cliEnv(t)
	writeTestFile(t, filepath.Join(repoDir, "a.txt"), "alpha")
	writeTestFile(t, filepath.Join(repoDir, "sub", "b.txt"), "beta")

	if err := run([]string{"init", storeDir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := run([]string{"init", storeDir}); err == nil {
		t.Fatal("second init succeeded")
	}
	if err := run([]string{"snapshot", "--store", storeDir, "--repo", repoDir}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := run([]string{"verify", "--store", storeDir, "--full"}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	if err := run([]string{"restore", "--store", storeDir, "--dest", dest}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "sub", "b.txt"))
	if err != nil || string(data) != "beta" {
		t.Fatalf("restored file = %q, %v", data, err)
	}

	// Two-way sync into a second machine.
	other := filepath.Join(t.TempDir(), "repo-b")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sync", "--direct", "--store", storeDir, "--repo", other}); err != nil {
		t.Fatalf("sync --direct: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(other, "a.txt"))
	if err != nil || string(data) != "alpha" {
		t.Fatalf("pulled file = %q, %v", data, err)
	}
}

func TestCLIGCAndTrash(t *testing.T) {
	storeDir, repoDir := cliEnv(t)
	writeTestFile(t, filepath.Join(repoDir, "a.txt"), "alpha")
	if err := run([]string{"init", storeDir}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"snapshot", "--store", storeDir, "--repo", repoDir}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"gc", "--store", storeDir, "--daily", "1", "--weekly", "0", "--monthly", "0"}); err != nil {
		t.Fatalf("gc: %v", err)
	}
	if err := run([]string{"trash", "list", "--store", storeDir}); err != nil {
		t.Fatalf("trash list: %v", err)
	}
	if err := run([]string{"trash", "empty", "--store", storeDir, "--dry-run"}); err != nil {
		t.Fatalf("trash empty: %v", err)
	}

	st, err := store.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Latest(); err != nil {
		t.Fatalf("latest after gc: %v", err)
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	if err := run([]string{"definitely-not-a-command"}); err == nil {
		t.Fatal("unknown command accepted")
	}
	if err := run([]string{"trash", "--help"}); err != nil {
		t.Fatalf("trash help: %v", err)
	}
}

func TestCLIConfigRejectsBadValues(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeTestFile(t, cfgPath, "[repo]\npath = \"~/Sync\"\n\n[sync]\nmode = \"sideways\"\n")
	allowAnyFS(t)
	err := run([]string{"snapshot", "--config", cfgPath,
		"--store", t.TempDir(), "--repo", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "unknown value") {
		t.Fatalf("bad mode accepted: %v", err)
	}
}
