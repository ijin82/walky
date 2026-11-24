package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/blake3"

	"walky/internal/store"
)

func TestBuildPlan(t *testing.T) {
	e := func(path, hash string) store.Entry {
		return store.Entry{Path: path, Type: store.TypeFile, Hash: hash, Size: 1, Mode: 0o644}
	}
	mk := func(entries ...store.Entry) *store.Manifest {
		return &store.Manifest{Entries: entries}
	}
	base := mk(
		e("same", "h1"),
		e("mod-local", "h1"),
		e("mod-remote", "h1"),
		e("both", "h1"),
		e("del-local", "h1"),
		e("del-remote", "h1"),
	)
	local := mk(
		e("same", "h1"),
		e("mod-local", "h2"),
		e("mod-remote", "h1"),
		e("both", "h2"),
		e("del-remote", "h1"),
	)
	remote := mk(
		e("same", "h1"),
		e("mod-local", "h1"),
		e("mod-remote", "h2"),
		e("both", "h3"),
		e("del-local", "h1"),
	)

	plan := buildPlan(local, remote, base, ConflictCopy)
	got := make(map[string]planKind, len(plan))
	for _, item := range plan {
		got[item.Path] = item.Kind
	}
	want := map[string]planKind{
		"mod-local":  actPush,
		"mod-remote": actPull,
		"both":       actConflictRemote,
		"del-local":  actDeleteRemote,
		"del-remote": actDeleteLocal,
	}
	if len(got) != len(want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Fatalf("%s: kind %v, want %v", path, got[path], kind)
		}
	}
	if _, ok := got["same"]; ok {
		t.Fatal("unchanged path ended up in the plan")
	}
}

func TestTwoWayConvergence(t *testing.T) {
	env := newSyncEnv(t)
	env.write(t, "A", "a.txt", "alpha")
	env.sync(t, "A")

	if _, err := os.Stat(filepath.Join(env.repo("B"), "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("B received the file before syncing: %v", err)
	}
	env.sync(t, "B")
	env.expect(t, "B", "a.txt", "alpha")

	env.write(t, "B", "sub/b.txt", "beta")
	env.sync(t, "B")
	env.sync(t, "A")
	env.expect(t, "A", "sub/b.txt", "beta")

	if err := os.Remove(filepath.Join(env.repo("A"), "a.txt")); err != nil {
		t.Fatal(err)
	}
	env.sync(t, "A")
	env.sync(t, "B")
	if _, err := os.Stat(filepath.Join(env.repo("B"), "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("deletion did not propagate: %v", err)
	}
	trash, err := filepath.Glob(filepath.Join(env.trash("B"), "*", "a.txt"))
	if err != nil || len(trash) != 1 {
		t.Fatalf("deleted file is not in the trash: %v, %v", trash, err)
	}
}

func TestConflictKeepsBoth(t *testing.T) {
	env := newSyncEnv(t)
	env.write(t, "A", "note.txt", "v1")
	env.sync(t, "A")
	env.sync(t, "B")

	env.write(t, "A", "note.txt", "from-a")
	env.sync(t, "A")
	env.write(t, "B", "note.txt", "from-b")
	res := env.sync(t, "B")

	if res.Conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", res.Conflicts)
	}
	env.expect(t, "B", "note.txt", "from-a")
	matches, err := filepath.Glob(filepath.Join(env.repo("B"), "note.conflict-hostB-*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("conflict copy = %v, %v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil || string(data) != "from-b" {
		t.Fatalf("conflict copy content = %q, %v", data, err)
	}

	// The conflict copy is pushed like any other file.
	env.sync(t, "A")
	matches, err = filepath.Glob(filepath.Join(env.repo("A"), "note.conflict-hostB-*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("conflict copy did not reach A: %v, %v", matches, err)
	}
}

func TestRandomConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("random convergence test skipped in -short mode")
	}
	env := newSyncEnv(t)
	rng := rand.New(rand.NewSource(42))
	files := []string{"a.txt", "b.txt", "dir/c.txt", "dir/d.txt", "e.txt"}

	for i := 0; i < 2000; i++ {
		side := "A"
		if rng.Intn(2) == 0 {
			side = "B"
		}
		name := files[rng.Intn(len(files))]
		path := filepath.Join(env.repo(side), filepath.FromSlash(name))
		switch rng.Intn(3) {
		case 0, 1:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			data := fmt.Sprintf("%s-%d", side, i)
			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		case 2:
			_ = os.Remove(path)
		}
		if i%5 == 0 {
			env.sync(t, side)
		}
	}
	env.sync(t, "A")
	env.sync(t, "B")
	env.sync(t, "A")
	env.sync(t, "B")

	a := env.tree(t, "A")
	b := env.tree(t, "B")
	if len(a) != len(b) {
		t.Fatalf("trees diverged: A has %d files, B has %d", len(a), len(b))
	}
	for path, hash := range a {
		if b[path] != hash {
			t.Fatalf("trees diverged at %s: %s vs %s", path, hash, b[path])
		}
	}
	env.verifyStore(t)
}

type syncEnv struct {
	t         *testing.T
	root      string
	store     string
	repos     map[string]string
	bases     map[string]string
	trashDirs map[string]string
}

func newSyncEnv(t *testing.T) *syncEnv {
	t.Helper()
	root := t.TempDir()
	storeRoot := filepath.Join(root, "store")
	if _, err := store.Init(storeRoot, false); err != nil {
		t.Fatal(err)
	}
	e := &syncEnv{
		t: t, root: root, store: storeRoot,
		repos: map[string]string{}, bases: map[string]string{}, trashDirs: map[string]string{},
	}
	for _, side := range []string{"A", "B"} {
		e.repos[side] = filepath.Join(root, "repo"+side)
		e.bases[side] = filepath.Join(root, "base"+side+".jsonl.zst")
		e.trashDirs[side] = filepath.Join(root, "trash"+side)
		if err := os.MkdirAll(e.repos[side], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *syncEnv) repo(side string) string { return e.repos[side] }

func (e *syncEnv) write(t *testing.T, side, rel, content string) {
	t.Helper()
	path := filepath.Join(e.repos[side], filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *syncEnv) sync(t *testing.T, side string) Result {
	t.Helper()
	res, err := Sync(context.Background(), Options{
		StoreRoot: e.store,
		RepoPath:  e.repos[side],
		Mode:      ModeTwoWay,
		Conflict:  ConflictCopy,
		BasePath:  e.bases[side],
		TrashDir:  e.trashDirs[side],
		Machine:   "machine-" + side,
		Hostname:  "host" + side,
	}, nil)
	if err != nil {
		t.Fatalf("sync %s: %v", side, err)
	}
	return res
}

func (e *syncEnv) expect(t *testing.T, side, rel, content string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.repos[side], filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s/%s: %v", side, rel, err)
	}
	if string(data) != content {
		t.Fatalf("%s/%s = %q, want %q", side, rel, data, content)
	}
}

func (e *syncEnv) trash(side string) string { return e.trashDirs[side] }

func (e *syncEnv) tree(t *testing.T, side string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := e.repos[side]
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := blake3.Sum256(data)
		out[rel] = fmt.Sprintf("%x", sum)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *syncEnv) verifyStore(t *testing.T) {
	t.Helper()
	st, err := store.Open(e.store)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.Verify(true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("store is corrupt: %+v", rep)
	}
}

func TestConflictLocalWins(t *testing.T) {
	env := newSyncEnv(t)
	env.write(t, "A", "x.txt", "v1")
	env.sync(t, "A")
	env.sync(t, "B")
	env.write(t, "A", "x.txt", "from-a")
	env.sync(t, "A")
	env.write(t, "B", "x.txt", "from-b")

	res, err := Sync(context.Background(), Options{
		StoreRoot: env.store, RepoPath: env.repo("B"), Mode: ModeTwoWay,
		Conflict: ConflictLocalWins, BasePath: env.bases["B"], TrashDir: env.trash("B"),
		Machine: "machine-B", Hostname: "hostB",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflicts != 1 {
		t.Fatalf("conflicts = %d", res.Conflicts)
	}
	env.expect(t, "B", "x.txt", "from-b")
	env.sync(t, "A")
	env.expect(t, "A", "x.txt", "from-b")
}

func TestHeadChangedIsReported(t *testing.T) {
	env := newSyncEnv(t)
	env.write(t, "A", "a.txt", "alpha")
	env.sync(t, "A")
	env.write(t, "B", "b.txt", "beta")
	env.sync(t, "B")

	st, err := store.Open(env.store)
	if err != nil {
		t.Fatal(err)
	}
	head, err := st.Head()
	if err != nil {
		t.Fatal(err)
	}
	stale := head.Gen - 1
	m := &store.Manifest{Entries: []store.Entry{{
		Path: "fake.txt", Type: store.TypeDir, Mode: 0o755,
	}}}
	if _, err := st.Commit(m, store.CommitOptions{ExpectParent: &stale}); err == nil {
		t.Fatal("commit with a stale parent succeeded")
	}
}

func TestBuildRecoveryPlan(t *testing.T) {
	e := func(path, hash string) store.Entry {
		return store.Entry{Path: path, Type: store.TypeFile, Hash: hash, Size: 1, Mode: 0o644}
	}
	local := &store.Manifest{Entries: []store.Entry{
		e("same.txt", "h1"), e("local-only.txt", "h2"), e("both.txt", "h3"),
	}}
	remote := &store.Manifest{Entries: []store.Entry{
		e("same.txt", "h1"), e("remote-only.txt", "h4"), e("both.txt", "h5"),
	}}
	plan := buildRecoveryPlan(local, remote)
	kinds := map[string]planKind{}
	for _, item := range plan {
		kinds[item.Path] = item.Kind
	}
	if kind := kinds["local-only.txt"]; kind != actPush {
		t.Fatalf("local-only = %v", kind)
	}
	if kind := kinds["remote-only.txt"]; kind != actPull {
		t.Fatalf("remote-only = %v", kind)
	}
	if kind := kinds["both.txt"]; kind != actConflictRemote {
		t.Fatalf("both = %v", kind)
	}
	for _, item := range plan {
		if item.Kind == actDeleteLocal || item.Kind == actDeleteRemote {
			t.Fatalf("recovery plan contains a deletion: %+v", item)
		}
	}
}

func TestDifferentStoreNeedsRecovery(t *testing.T) {
	env := newSyncEnv(t)
	old, err := store.Open(env.store)
	if err != nil {
		t.Fatal(err)
	}
	oldID := old.Marker().RepoID
	env.write(t, "A", "keep.txt", "keep")
	env.write(t, "A", "gone.txt", "bye")
	if _, err := Sync(context.Background(), Options{
		StoreRoot: env.store, RepoPath: env.repo("A"), Mode: ModeTwoWay,
		Conflict: ConflictCopy, BasePath: env.bases["A"], TrashDir: env.trash("A"),
		Machine: "A", Hostname: "hostA", StoreRepoID: oldID,
	}, nil); err != nil {
		t.Fatal(err)
	}

	newRoot := filepath.Join(env.root, "store-new")
	newStore, err := store.Init(newRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	newID := newStore.Marker().RepoID
	if newID == oldID {
		t.Fatal("new store reused the old repo_id")
	}
	if err := os.Remove(filepath.Join(env.repo("A"), "gone.txt")); err != nil {
		t.Fatal(err)
	}
	env.write(t, "A", "keep.txt", "keep-v2")

	opt := Options{
		StoreRoot: newRoot, RepoPath: env.repo("A"), Mode: ModeTwoWay,
		Conflict: ConflictCopy, BasePath: env.bases["A"], TrashDir: env.trash("A"),
		Machine: "A", Hostname: "hostA", StoreRepoID: newID,
	}
	if _, err := Sync(context.Background(), opt, nil); !errors.Is(err, ErrDifferentStore) {
		t.Fatalf("normal sync error = %v, want ErrDifferentStore", err)
	}

	opt.Recover = true
	preview, err := Preview(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Deletes != 0 || preview.Push < 1 || preview.Total == 0 {
		t.Fatalf("preview = %+v", preview)
	}
	res, err := Sync(context.Background(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 {
		t.Fatalf("recovery deleted %d files", res.Deleted)
	}
	env.expect(t, "A", "keep.txt", "keep-v2")
	if _, err := os.Stat(filepath.Join(env.repo("A"), "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("recovery resurrected a locally deleted file: %v", err)
	}
	if got := loadBaseRepoID(env.bases["A"]); got != newID {
		t.Fatalf("base repo_id = %q, want %q", got, newID)
	}
	opt.Recover = false
	if _, err := Sync(context.Background(), opt, nil); err != nil {
		t.Fatalf("normal sync after recovery: %v", err)
	}
}
