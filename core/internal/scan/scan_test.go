package scan

import (
	"os"
	"path/filepath"
	"testing"

	"walky/internal/index"
	"walky/internal/store"
)

func TestWalkBasics(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "sub", "b.txt"), "bb")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	res, err := Walk(root, Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", res.Skipped)
	}

	byPath := map[string]store.Entry{}
	for _, e := range res.Manifest.Entries {
		byPath[e.Path] = e
	}
	if len(byPath) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(byPath), byPath)
	}
	if byPath["a.txt"].Type != store.TypeFile || byPath["a.txt"].Size != 1 {
		t.Fatalf("a.txt entry: %+v", byPath["a.txt"])
	}
	if byPath["empty"].Type != store.TypeDir {
		t.Fatalf("empty entry: %+v", byPath["empty"])
	}
	if link := byPath["link"]; link.Type != store.TypeSymlink || link.Target != "a.txt" {
		t.Fatalf("link entry: %+v", link)
	}
	if !sorted(res.Manifest.Entries) {
		t.Fatal("manifest is not sorted")
	}
}

func TestWalkExcludes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "keep.txt"), "k")
	mustWrite(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "x")
	mustWrite(t, filepath.Join(root, "sub", "node_modules", "y.js"), "y")
	mustWrite(t, filepath.Join(root, "sub", "target", "debug.bin"), "z")
	mustWrite(t, filepath.Join(root, "sub", "keep.go"), "g")

	res, err := Walk(root, Options{Excludes: []string{"**/node_modules", "**/target"}})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	got := map[string]bool{}
	for _, e := range res.Manifest.Entries {
		got[e.Path] = true
	}
	for _, p := range []string{"keep.txt", "sub", "sub/keep.go"} {
		if !got[p] {
			t.Errorf("missing %q in %v", p, got)
		}
	}
	for _, p := range []string{"node_modules", "sub/node_modules", "sub/target"} {
		if got[p] {
			t.Errorf("excluded %q was scanned", p)
		}
	}
}

func TestWalkMaxFileSize(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "small"), "123")
	mustWrite(t, filepath.Join(root, "large"), "1234567890")

	res, err := Walk(root, Options{MaxFileSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manifest.Entries) != 1 || res.Manifest.Entries[0].Path != "small" {
		t.Fatalf("entries: %+v", res.Manifest.Entries)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Path != "large" {
		t.Fatalf("skipped: %+v", res.Skipped)
	}
}

func TestWalkDeterministic(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"c", "a", "b"} {
		mustWrite(t, filepath.Join(root, p), p)
	}
	first, err := Walk(root, Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Walk(root, Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	firstData, _ := store.EncodeManifest(&first.Manifest)
	secondData, _ := store.EncodeManifest(&second.Manifest)
	if string(firstData) != string(secondData) {
		t.Fatal("scan results depend on worker count")
	}
}

func TestWalkRejectsFileRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	mustWrite(t, f, "x")
	if _, err := Walk(f, Options{}); err == nil {
		t.Fatal("expected error for non-directory root")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sorted(entries []store.Entry) bool {
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Path >= entries[i].Path {
			return false
		}
	}
	return true
}

func TestWalkIndexCache(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := Walk(root, Options{Index: db})
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached != 0 || first.Hashed != 2 {
		t.Fatalf("first scan: cached=%d hashed=%d", first.Cached, first.Hashed)
	}

	second, err := Walk(root, Options{Index: db})
	if err != nil {
		t.Fatal(err)
	}
	if second.Cached != 2 || second.Hashed != 0 {
		t.Fatalf("second scan: cached=%d hashed=%d", second.Cached, second.Hashed)
	}
	a, _ := store.EncodeManifest(&first.Manifest)
	b, _ := store.EncodeManifest(&second.Manifest)
	if string(a) != string(b) {
		t.Fatal("cached scan produced a different manifest")
	}

	// A content change must invalidate only that file.
	mustWrite(t, filepath.Join(root, "a.txt"), "changed content")
	third, err := Walk(root, Options{Index: db})
	if err != nil {
		t.Fatal(err)
	}
	if third.Cached != 1 || third.Hashed != 1 {
		t.Fatalf("third scan: cached=%d hashed=%d", third.Cached, third.Hashed)
	}

	// A deleted file must be pruned from the index.
	if err := os.Remove(filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Walk(root, Options{Index: db}); err != nil {
		t.Fatal(err)
	}
	recs, err := db.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := recs["b.txt"]; ok {
		t.Fatal("deleted file is still cached")
	}
}

func TestWalkForceRescan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := Walk(root, Options{Index: db}); err != nil {
		t.Fatal(err)
	}
	res, err := Walk(root, Options{Index: db, ForceRescan: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cached != 0 || res.Hashed != 1 {
		t.Fatalf("force rescan: cached=%d hashed=%d", res.Cached, res.Hashed)
	}
}
