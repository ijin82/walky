package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptionStatus(t *testing.T) {
	dir := t.TempDir()
	mountinfo := filepath.Join(dir, "mountinfo")
	data := `36 25 8:1 / /mnt/plain rw - ext4 /dev/sdb1 rw
37 25 8:2 / /mnt/crypt rw - ext4 /dev/dm-0 rw
38 25 0:5 / /proc rw - proc proc rw
`
	if err := os.WriteFile(mountinfo, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	sysfs := filepath.Join(dir, "sys")
	if err := os.MkdirAll(filepath.Join(sysfs, "class", "block", "dm-0", "dm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysfs, "class", "block", "dm-0", "dm", "uuid"),
		[]byte("CRYPT-LUKS2-1234-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path      string
		encrypted bool
		known     bool
	}{
		{"/mnt/plain", false, true},
		{"/mnt/plain/sub/dir", false, true},
		{"/mnt/crypt", true, true},
		{"/mnt/crypt/sub", true, true},
		{"/somewhere/else", false, false},
	}
	for _, c := range cases {
		enc, known := encryptionStatus(mountinfo, sysfs, c.path)
		if enc != c.encrypted || known != c.known {
			t.Errorf("%s: encrypted=%v known=%v, want %v/%v", c.path, enc, known, c.encrypted, c.known)
		}
	}
}

func TestEncryptionStatusStackedDevice(t *testing.T) {
	dir := t.TempDir()
	mountinfo := filepath.Join(dir, "mountinfo")
	if err := os.WriteFile(mountinfo, []byte("36 25 8:1 / /mnt/lvm rw - ext4 /dev/dm-1 rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sysfs := filepath.Join(dir, "sys")
	// dm-1 (LVM) -> dm-0 (LUKS)
	if err := os.MkdirAll(filepath.Join(sysfs, "class", "block", "dm-1", "slaves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/dm-0", filepath.Join(sysfs, "class", "block", "dm-1", "slaves", "dm-0")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sysfs, "class", "block", "dm-0", "dm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysfs, "class", "block", "dm-0", "dm", "uuid"),
		[]byte("CRYPT-LUKS2-5678-stack\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	enc, known := encryptionStatus(mountinfo, sysfs, "/mnt/lvm")
	if !enc || !known {
		t.Fatalf("stacked LUKS not detected: encrypted=%v known=%v", enc, known)
	}
}
