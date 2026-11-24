package store

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// extMagic is EXT4_SUPER_MAGIC (shared by ext2/ext3/ext4).
const extMagic = 0xEF53

// CheckFilesystem verifies that path lives on an ext4 filesystem. Walky stores
// rely on POSIX permissions, symlinks and atomic renames, so other filesystems
// are rejected instead of silently degrading.
//
// The path may not exist yet: the nearest existing ancestor is probed, which
// lets `walky init` validate a fresh store directory.
func CheckFilesystem(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	probe := abs
	for {
		if _, statErr := os.Stat(probe); statErr == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	var st unix.Statfs_t
	if err := unix.Statfs(probe, &st); err != nil {
		return fmt.Errorf("statfs %s: %w", probe, err)
	}
	if uint32(st.Type) != extMagic {
		return fmt.Errorf("unsupported filesystem on %s (type 0x%x): walky requires ext4", abs, uint32(st.Type))
	}
	return nil
}
