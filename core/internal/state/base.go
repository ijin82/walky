package state

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// BasePath returns the local base-manifest path for a repository.
func BasePath(repo string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(dir, "base-"+hex.EncodeToString(sum[:8])+".jsonl.zst"), nil
}

// TrashDir returns the local trash root for files deleted by a sync.
func TrashDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "trash"), nil
}

// EnsureDir creates a directory with private permissions.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o700)
}
