package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"walky/internal/store"
)

func TestRunSnapshot(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "a.txt"), "alpha")
	write(t, filepath.Join(repo, "sub", "b.txt"), "beta")

	storeRoot := t.TempDir()
	if _, err := store.Init(storeRoot, false); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(t.TempDir(), "index.db")

	var phases []string
	res, err := Run(context.Background(), Options{
		StoreRoot: storeRoot,
		RepoPath:  repo,
		IndexPath: idx,
		Machine:   "test",
	}, func(p Progress) { phases = append(phases, p.Phase) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Gen != 1 || !res.Changed || res.Objects != 2 {
		t.Fatalf("result = %+v", res)
	}
	if len(phases) == 0 || phases[0] != "scan" {
		t.Fatalf("phases = %v", phases)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.Verify(true)
	if err != nil || !rep.OK() {
		t.Fatalf("verify = %+v, %v", rep, err)
	}

	// No changes: a no-op commit.
	res, err = Run(context.Background(), Options{
		StoreRoot: storeRoot, RepoPath: repo, IndexPath: idx, Machine: "test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Objects != 0 || res.Gen != 1 {
		t.Fatalf("second run = %+v", res)
	}

	// One new file produces one new object.
	write(t, filepath.Join(repo, "c.txt"), "gamma")
	res, err = Run(context.Background(), Options{
		StoreRoot: storeRoot, RepoPath: repo, IndexPath: idx, Machine: "test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Objects != 1 || res.Gen != 2 {
		t.Fatalf("third run = %+v", res)
	}
}

func TestRunCancelled(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "a.txt"), "alpha")
	storeRoot := t.TempDir()
	if _, err := store.Init(storeRoot, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Options{StoreRoot: storeRoot, RepoPath: repo}, nil); err == nil {
		t.Fatal("expected a cancellation error")
	}
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Head(); !errors.Is(err, store.ErrNoHead) {
		t.Fatalf("HEAD moved on a cancelled run: %v", err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
