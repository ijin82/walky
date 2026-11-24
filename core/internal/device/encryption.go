package device

import (
	"os"
	"path/filepath"
	"strings"
)

// EncryptionStatus reports whether the filesystem containing path sits inside a
// dm-crypt/LUKS container. known is false when no mount covers the path.
//
// It is advisory only: Walky never refuses an unencrypted drive.
func EncryptionStatus(path string) (encrypted, known bool) {
	return encryptionStatus("/proc/self/mountinfo", "/sys", path)
}

func encryptionStatus(mountinfoPath, sysfsRoot, path string) (bool, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, false
	}
	mounts, err := ReadMounts(mountinfoPath)
	if err != nil {
		return false, false
	}
	var best Mount
	for _, m := range mounts {
		if abs != m.Point && !strings.HasPrefix(abs, m.Point+"/") {
			continue
		}
		if len(m.Point) > len(best.Point) {
			best = m
		}
	}
	if best.Point == "" {
		return false, false
	}
	return deviceEncrypted(sysfsRoot, best.Source), true
}

func deviceEncrypted(sysfsRoot, source string) bool {
	if !strings.HasPrefix(source, "/dev/") {
		return false
	}
	resolved := source
	if target, err := filepath.EvalSymlinks(source); err == nil {
		resolved = target
	}
	return blockEncrypted(sysfsRoot, filepath.Base(resolved), 0)
}

// blockEncrypted walks the device and its slaves, so LVM or other stacking on
// top of LUKS is still detected.
func blockEncrypted(sysfsRoot, name string, depth int) bool {
	if depth > 4 {
		return false
	}
	dir := filepath.Join(sysfsRoot, "class", "block", name)
	if data, err := os.ReadFile(filepath.Join(dir, "dm", "uuid")); err == nil {
		if strings.HasPrefix(string(data), "CRYPT-LUKS") {
			return true
		}
	}
	slaves, err := os.ReadDir(filepath.Join(dir, "slaves"))
	if err != nil {
		return false
	}
	for _, slave := range slaves {
		if blockEncrypted(sysfsRoot, slave.Name(), depth+1) {
			return true
		}
	}
	return false
}
