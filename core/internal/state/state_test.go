package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatePathsAndLocalTrash(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(xdg, "walky") {
		t.Fatalf("Dir = %q", dir)
	}

	idx1, err := IndexPath("/tmp/repo-a")
	if err != nil {
		t.Fatal(err)
	}
	idx2, err := IndexPath("/tmp/repo-b")
	if err != nil {
		t.Fatal(err)
	}
	if idx1 == idx2 || !strings.HasSuffix(idx1, ".db") {
		t.Fatalf("IndexPath = %q vs %q", idx1, idx2)
	}

	base, err := BasePath("/tmp/repo-a")
	if err != nil || !strings.HasSuffix(base, ".jsonl.zst") {
		t.Fatalf("BasePath = %q, %v", base, err)
	}

	// Empty trash returns zero stats without error.
	st, err := LocalTrashStats()
	if err != nil || st.Files != 0 || st.Bytes != 0 {
		t.Fatalf("empty LocalTrashStats = %+v, %v", st, err)
	}

	trashDir, err := TrashDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(filepath.Join(trashDir, "20260101-000000")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trashDir, "20260101-000000", "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err = LocalTrashStats()
	if err != nil || st.Files != 1 || st.Bytes != 5 {
		t.Fatalf("LocalTrashStats = %+v, %v", st, err)
	}
	cleared, err := ClearLocalTrash()
	if err != nil || cleared.Files != 1 || cleared.Bytes != 5 {
		t.Fatalf("ClearLocalTrash = %+v, %v", cleared, err)
	}
	st, err = LocalTrashStats()
	if err != nil || st.Files != 0 {
		t.Fatalf("post-clear LocalTrashStats = %+v, %v", st, err)
	}
}
