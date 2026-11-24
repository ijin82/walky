package index

import (
	"path/filepath"
	"strings"
	"testing"
)

func rec(path string) Record {
	return Record{
		Path: path, Dev: 1, Ino: 2, Size: 3, MtimeNS: 4, CtimeNS: 5,
		Mode: 0o644, Hash: strings.Repeat("a", 64),
	}
}

func TestApplyLoadPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.db")
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply([]Record{rec("a"), rec("b")}, map[string]struct{}{"a": {}, "b": {}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a"].Hash != rec("a").Hash {
		t.Fatalf("loaded %+v", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err = db.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("persisted %d records, want 2", len(got))
	}
}

func TestApplyUpdatesAndPrunes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Apply([]Record{rec("a"), rec("b")}, map[string]struct{}{"a": {}, "b": {}}); err != nil {
		t.Fatal(err)
	}
	updated := rec("a")
	updated.Hash = strings.Repeat("c", 64)
	updated.Size = 42
	if err := db.Apply([]Record{updated, rec("c")}, map[string]struct{}{"a": {}, "c": {}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (a and c)", len(got))
	}
	if got["a"].Hash != updated.Hash || got["a"].Size != 42 {
		t.Fatalf("a not updated: %+v", got["a"])
	}
	if _, ok := got["b"]; ok {
		t.Fatal("stale record b survived pruning")
	}
}

func TestCount(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n, err := db.Count()
	if err != nil || n != 0 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	if err := db.Apply([]Record{rec("a")}, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ = db.Count(); n != 1 {
		t.Fatalf("Count = %d, want 1", n)
	}
}
