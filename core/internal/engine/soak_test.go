package engine

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"walky/internal/store"
)

// TestSoakMultiMachineGCAndCancel exercises 3 simulated machines sharing one
// store with interleaved file/directory mutations, periodic GC and cancelled
// syncs. Run with WALKY_SOAK=1 for a long stress run.
func TestSoakMultiMachineGCAndCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test skipped in -short mode")
	}
	steps := 300
	if os.Getenv("WALKY_SOAK") != "" {
		steps = 4000
	}

	root := t.TempDir()
	storeRoot := filepath.Join(root, "store")
	if _, err := store.Init(storeRoot, false); err != nil {
		t.Fatal(err)
	}
	sides := []string{"A", "B", "C"}
	env := &syncEnv{
		t: t, root: root, store: storeRoot,
		repos: map[string]string{}, bases: map[string]string{}, trashDirs: map[string]string{},
	}
	for _, side := range sides {
		env.repos[side] = filepath.Join(root, "repo"+side)
		env.bases[side] = filepath.Join(root, "base"+side+".jsonl.zst")
		env.trashDirs[side] = filepath.Join(root, "trash"+side)
		if err := os.MkdirAll(env.repos[side], 0o755); err != nil {
			t.Fatal(err)
		}
	}

	rng := rand.New(rand.NewSource(20261125))
	paths := []string{
		"a.txt", "b.txt",
		"sub1/c.txt", "sub1/d.txt",
		"sub2/deep/e.txt", "sub2/f.txt",
	}

	for i := 0; i < steps; i++ {
		side := sides[rng.Intn(len(sides))]
		rel := paths[rng.Intn(len(paths))]
		full := filepath.Join(env.repo(side), filepath.FromSlash(rel))

		switch rng.Intn(5) {
		case 0, 1, 2:
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			data := fmt.Sprintf("%s-step-%d-%d", side, i, rng.Intn(1000))
			if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		case 3:
			_ = os.Remove(full)
		case 4:
			// Occasionally remove a whole subdirectory to exercise directory deletion.
			_ = os.RemoveAll(filepath.Join(env.repo(side), "sub1"))
		}

		if i%7 == 0 {
			env.sync(t, side)
		}
		if i%29 == 0 {
			// Simulate an interrupted sync via an already-cancelled context.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = Sync(ctx, Options{
				StoreRoot: env.store,
				RepoPath:  env.repos[side],
				Mode:      ModeTwoWay,
				Conflict:  ConflictCopy,
				BasePath:  env.bases[side],
				TrashDir:  env.trashDirs[side],
				Machine:   "machine-" + side,
				Hostname:  "host" + side,
			}, nil)
		}
		if i%41 == 0 {
			st, err := store.Open(env.store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.Head(); err == nil {
				if _, err := st.GC(store.GCOptions{
					KeepDaily: 2,
					TrashDays: 0,
					Now:       time.Now().Add(48 * time.Hour),
				}); err != nil {
					t.Fatalf("GC at step %d: %v", i, err)
				}
			}
		}
	}

	// Final convergence rounds across all three machines.
	for round := 0; round < 3; round++ {
		for _, side := range sides {
			env.sync(t, side)
		}
	}

	ref := env.tree(t, "A")
	for _, side := range []string{"B", "C"} {
		got := env.tree(t, side)
		if len(got) != len(ref) {
			t.Fatalf("trees diverged: A has %d files, %s has %d", len(ref), side, len(got))
		}
		for p, h := range ref {
			if got[p] != h {
				t.Fatalf("trees diverged at %s: A=%s %s=%s", p, h, side, got[p])
			}
		}
	}
	env.verifyStore(t)
}
