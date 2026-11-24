package store_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"walky/internal/scan"
	"walky/internal/store"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestInitOpen(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Init(dir, false)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.MarkerName)); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if len(st.Marker().RepoID) != 32 {
		t.Fatalf("repo id %q", st.Marker().RepoID)
	}
	if _, err := store.Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Init(dir, false); !errors.Is(err, store.ErrAlreadyInitialized) {
		t.Fatalf("second Init = %v, want ErrAlreadyInitialized", err)
	}
	if _, err := st.Head(); !errors.Is(err, store.ErrNoHead) {
		t.Fatalf("Head on empty store = %v, want ErrNoHead", err)
	}
}

func TestInitRefusesNonEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "keep.txt"), "x", 0o644)
	if _, err := store.Init(dir, false); !errors.Is(err, store.ErrNotEmpty) {
		t.Fatalf("Init = %v, want ErrNotEmpty", err)
	}
	if _, err := store.Init(dir, true); err != nil {
		t.Fatalf("Init --force: %v", err)
	}
}

func TestOpenRejectsForeignDirectory(t *testing.T) {
	if _, err := store.Open(t.TempDir()); !errors.Is(err, store.ErrNotInitialized) {
		t.Fatalf("Open = %v, want ErrNotInitialized", err)
	}
}

func TestRoundTrip(t *testing.T) {
	storeDir := t.TempDir()
	src := t.TempDir()

	writeFile(t, filepath.Join(src, "hello.txt"), "hello walky\n", 0o644)
	writeFile(t, filepath.Join(src, "bin", "tool.sh"), "#!/bin/sh\necho ok\n", 0o755)
	big := bytes.Repeat([]byte("0123456789abcdef"), 16*1024) // 256 KiB
	writeFile(t, filepath.Join(src, "data", "big.bin"), string(big), 0o600)
	if err := os.MkdirAll(filepath.Join(src, "empty", "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("hello.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(1700000000, 123456789)
	for _, p := range []string{"hello.txt", "bin/tool.sh", "data/big.bin"} {
		if err := os.Chtimes(filepath.Join(src, p), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Init(storeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := scan.Walk(src, scan.Options{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	uploadAll(t, st, src, &res.Manifest)

	commit, err := st.Commit(&res.Manifest, store.CommitOptions{Machine: "test-machine"})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !commit.Changed || commit.Gen != 1 {
		t.Fatalf("commit = %+v, want gen 1 changed", commit)
	}

	// A second identical commit must be a no-op.
	again, err := st.Commit(&res.Manifest, store.CommitOptions{Machine: "test-machine"})
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if again.Changed || again.Gen != 1 {
		t.Fatalf("second commit = %+v, want no-op gen 1", again)
	}

	m, meta, err := st.Latest()
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if meta.Gen != 1 || meta.Entries != len(m.Entries) {
		t.Fatalf("meta mismatch: %+v", meta)
	}

	dest := t.TempDir()
	if err := st.Restore(dest, m, false); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Contents and modes.
	for _, p := range []string{"hello.txt", "bin/tool.sh", "data/big.bin"} {
		want, err := os.ReadFile(filepath.Join(src, p))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, p))
		if err != nil {
			t.Fatalf("restored %s: %v", p, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("%s content differs", p)
		}
		si, _ := os.Stat(filepath.Join(src, p))
		di, _ := os.Stat(filepath.Join(dest, p))
		if si.Mode() != di.Mode() {
			t.Fatalf("%s mode = %v, want %v", p, di.Mode(), si.Mode())
		}
		if !si.ModTime().Equal(di.ModTime()) {
			t.Fatalf("%s mtime = %v, want %v", p, di.ModTime(), si.ModTime())
		}
	}

	// Symlink and empty directories.
	target, err := os.Readlink(filepath.Join(dest, "link"))
	if err != nil || target != "hello.txt" {
		t.Fatalf("symlink = %q, %v", target, err)
	}
	for _, p := range []string{"empty", "empty/nested"} {
		fi, err := os.Stat(filepath.Join(dest, p))
		if err != nil || !fi.IsDir() {
			t.Fatalf("directory %s not restored: %v", p, err)
		}
	}

	// Every file entry points at an existing object.
	for _, e := range m.Entries {
		if e.Type != store.TypeFile {
			continue
		}
		p, err := st.ObjectPath(e.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("object for %s: %v", e.Path, err)
		}
	}
}

func TestRestoreRefusesNonEmpty(t *testing.T) {
	st, m := storeWithOneFile(t)
	dest := t.TempDir()
	writeFile(t, filepath.Join(dest, "existing.txt"), "keep", 0o644)
	if err := st.Restore(dest, m, false); err == nil {
		t.Fatal("expected refusal for non-empty destination")
	}
	if err := st.Restore(dest, m, true); err != nil {
		t.Fatalf("restore --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "file.txt")); err != nil {
		t.Fatalf("file not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "existing.txt")); err != nil {
		t.Fatalf("existing file removed: %v", err)
	}
}

func TestCommitRejectsUnsafePaths(t *testing.T) {
	st, err := store.Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hash, _, err := st.PutObject(bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../evil", "/abs", "a/../../b", "."} {
		m := &store.Manifest{Entries: []store.Entry{{Path: p, Type: store.TypeFile, Hash: hash, Size: 1}}}
		if _, err := st.Commit(m, store.CommitOptions{}); err == nil {
			t.Fatalf("Commit accepted unsafe path %q", p)
		}
	}
}

func TestCommitRequiresObjects(t *testing.T) {
	st, err := store.Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	m := &store.Manifest{Entries: []store.Entry{{
		Path: "a.txt", Type: store.TypeFile, Size: 1,
		Hash: "0000000000000000000000000000000000000000000000000000000000000000",
	}}}
	if _, err := st.Commit(m, store.CommitOptions{}); err == nil {
		t.Fatal("commit without object must fail")
	}
	if _, err := st.Head(); !errors.Is(err, store.ErrNoHead) {
		t.Fatalf("HEAD moved: %v", err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	st, m := storeWithOneFile(t)
	if _, err := st.Verify(true); err != nil {
		t.Fatalf("verify clean: %v", err)
	}
	e := m.Entries[0]
	p, err := st.ObjectPath(e.Hash)
	if err != nil {
		t.Fatal(err)
	}
	// Same size, different content.
	data := bytes.Repeat([]byte("A"), int(e.Size))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := st.Verify(true)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.OK() || len(rep.Corrupt) != 1 {
		t.Fatalf("corruption not detected: %+v", rep)
	}
}

func TestVerifyDetectsManifestTamper(t *testing.T) {
	st, _ := storeWithOneFile(t)
	head, err := st.Head()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Root(), "snapshots", "00000001.jsonl.zst")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Verify(false); err == nil {
		t.Fatalf("tampered manifest accepted (head gen %d)", head.Gen)
	}
}

func TestLockIsExclusive(t *testing.T) {
	st, err := store.Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := st.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := st.Lock(); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("second Lock = %v, want ErrLocked", err)
	}
}

func TestRestoreDirectoryModes(t *testing.T) {
	storeDir := t.TempDir()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "ro", "f.txt"), "data", 0o400)
	if err := os.MkdirAll(filepath.Join(src, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Init(storeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := scan.Walk(src, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	uploadAll(t, st, src, &res.Manifest)
	if _, err := st.Commit(&res.Manifest, store.CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	m, _, err := st.Latest()
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := st.Restore(dest, m, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dest, "ro", "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fs.FileMode(0o400) != fi.Mode().Perm() {
		t.Fatalf("file mode = %v, want 0400", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Join(dest, "private"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", di.Mode().Perm())
	}
}

// storeWithOneFile creates a store with a single committed file.
func storeWithOneFile(t *testing.T) (*store.Store, *store.Manifest) {
	t.Helper()
	st, err := store.Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := st.PutObject(bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatal(err)
	}
	m := &store.Manifest{Entries: []store.Entry{{
		Path: "file.txt", Type: store.TypeFile, Size: size, Mode: 0o644, Hash: hash,
	}}}
	if _, err := st.Commit(m, store.CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	return st, m
}

func uploadAll(t *testing.T, st *store.Store, src string, m *store.Manifest) {
	t.Helper()
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.Type != store.TypeFile {
			continue
		}
		f, err := os.Open(filepath.Join(src, filepath.FromSlash(e.Path)))
		if err != nil {
			t.Fatal(err)
		}
		hash, size, err := st.PutObject(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if hash != e.Hash || size != e.Size {
			t.Fatalf("%s: object %s/%d, manifest %s/%d", e.Path, hash, size, e.Hash, e.Size)
		}
	}
}

func TestInitCreatesMissingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "WALKY")
	if _, err := store.Init(root, false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, store.MarkerName)); err != nil {
		t.Fatalf("marker: %v", err)
	}
}
