package store

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCheckFilesystem(t *testing.T) {
	dir := t.TempDir()
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		t.Fatal(err)
	}
	wantExt4 := uint32(st.Type) == extMagic

	if err := CheckFilesystem(dir); (err == nil) != wantExt4 {
		t.Fatalf("CheckFilesystem(%s) = %v, ext4=%v", dir, err, wantExt4)
	}

	// Missing paths are checked through their nearest existing ancestor.
	missing := filepath.Join(dir, "deep", "store")
	if err := CheckFilesystem(missing); (err == nil) != wantExt4 {
		t.Fatalf("CheckFilesystem(%s) = %v, ext4=%v", missing, err, wantExt4)
	}
}
