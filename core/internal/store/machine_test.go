package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMachineState(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := Machine{
		ID:       "0123456789abcdef0123456789abcdef",
		Hostname: "host-a",
		BaseGen:  7,
		LastSync: time.Unix(1700000000, 0).UTC(),
	}
	if err := st.SaveMachine(want); err != nil {
		t.Fatalf("SaveMachine: %v", err)
	}
	got, err := st.LoadMachine(want.ID)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	if got.ID != want.ID || got.BaseGen != want.BaseGen || got.Hostname != want.Hostname {
		t.Fatalf("machine = %+v, want %+v", got, want)
	}
	if _, err := st.LoadMachine("missing"); err == nil {
		t.Fatal("loaded a missing machine")
	}
}

func TestSaveTrash(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Path: "a.txt", Type: TypeFile, Size: 3, Hash: "00"}}
	if err := st.SaveTrash(4, entries); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.Root(), "trash", "00000004.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "a.txt" {
		t.Fatalf("trash = %+v", got)
	}
	if err := st.SaveTrash(5, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.Root(), "trash", "00000005.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty trash was written")
	}
}

func TestManifestFileRoundTrip(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := st.PutObject(bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Entries: []Entry{{
		Path: "dir/f.txt", Type: TypeFile, Size: size, Mode: 0o644, Hash: hash,
	}, {
		Path: "dir", Type: TypeDir, Mode: 0o755,
	}}}
	path := filepath.Join(t.TempDir(), "base.jsonl.zst")
	if err := WriteManifestFile(path, m); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %d", len(got.Entries))
	}
}

func TestCommitExpectParent(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := st.PutObject(bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Entries: []Entry{{
		Path: "x", Type: TypeFile, Size: size, Mode: 0o644, Hash: hash,
	}}}
	zero := int64(0)
	if _, err := st.Commit(m, CommitOptions{ExpectParent: &zero}); err != nil {
		t.Fatal(err)
	}
	changed := &Manifest{Entries: append([]Entry(nil), m.Entries...)}
	changed.Entries[0].Path = "y"
	if _, err := st.Commit(changed, CommitOptions{ExpectParent: &zero}); !errors.Is(err, ErrHeadChanged) {
		t.Fatalf("stale commit = %v, want ErrHeadChanged", err)
	}
	one := int64(1)
	res, err := st.Commit(m, CommitOptions{ExpectParent: &one})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatal("identical manifest reported as changed")
	}
}

func TestTrashRecordsAndClear(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Path: "a.txt", Type: TypeFile, Size: 3, Hash: "00"}}
	if err := st.SaveTrash(3, entries); err != nil {
		t.Fatal(err)
	}
	records, err := st.TrashRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Gen != 3 || len(records[0].Entries) != 1 {
		t.Fatalf("records = %+v", records)
	}
	removed, bytes, err := st.ClearTrash()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || bytes <= 0 {
		t.Fatalf("cleared %d records, %d bytes", removed, bytes)
	}
	records, err = st.TrashRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records after clear = %+v", records)
	}
}
