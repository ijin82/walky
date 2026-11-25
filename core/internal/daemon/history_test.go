package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"walky/internal/device"
	"walky/internal/store"
)

func TestHistoryBrowseAndRestore(t *testing.T) {
	storeRoot := t.TempDir()
	st, err := store.Init(storeRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	h1, s1, err := st.PutObject(strings.NewReader("v1-content"))
	if err != nil {
		t.Fatal(err)
	}
	h2, s2, err := st.PutObject(strings.NewReader("v2-content"))
	if err != nil {
		t.Fatal(err)
	}
	res1, err := st.Commit(&store.Manifest{Entries: []store.Entry{
		{Path: "docs/note.txt", Type: store.TypeFile, Size: s1, Mode: 0o644, Hash: h1},
		{Path: "other.txt", Type: store.TypeFile, Size: s1, Mode: 0o644, Hash: h1},
	}}, store.CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := st.Commit(&store.Manifest{Entries: []store.Entry{
		{Path: "docs/note.txt", Type: store.TypeFile, Size: s2, Mode: 0o644, Hash: h2},
	}}, store.CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	d := New(Config{RepoPath: repo, NoTray: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.dev = &device.Device{StoreRoot: storeRoot, MountPoint: storeRoot, Label: "WALKY"}

	snaps, err := d.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 || snaps[0].Gen != res2.Gen || snaps[1].Gen != res1.Gen {
		t.Fatalf("snapshots = %+v", snaps)
	}

	files, err := d.ListSnapshotFiles(res1.Gen, "NOTE", 50)
	if err != nil {
		t.Fatalf("ListSnapshotFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "docs/note.txt" {
		t.Fatalf("filtered files = %+v", files)
	}

	// Restore v1 of docs/note.txt into the local sync folder.
	restored, err := d.RestoreFile(res1.Gen, "docs/note.txt", "")
	if err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	data, err := os.ReadFile(restored)
	if err != nil || string(data) != "v1-content" {
		t.Fatalf("restored file = %q, %v", data, err)
	}
	if restored != filepath.Join(repo, "docs", "note.txt") {
		t.Fatalf("restored path = %q", restored)
	}
}
