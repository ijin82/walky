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

// TrashStats describes local trash contents.
type TrashStats struct {
	Files int
	Bytes int64
}

// LocalTrashStats measures the local trash without changing it.
func LocalTrashStats() (TrashStats, error) {
	dir, err := TrashDir()
	if err != nil {
		return TrashStats{}, err
	}
	var st TrashStats
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		st.Files++
		st.Bytes += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return TrashStats{}, nil
	}
	return st, err
}

// ClearLocalTrash removes everything under the local trash directory.
func ClearLocalTrash() (TrashStats, error) {
	dir, err := TrashDir()
	if err != nil {
		return TrashStats{}, err
	}
	st, err := LocalTrashStats()
	if err != nil {
		return st, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return st, err
	}
	return st, nil
}
