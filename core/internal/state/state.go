// Package state locates machine-local runtime state that must survive builds
// but never lives on the drive.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// Dir returns the Walky state directory (XDG_STATE_HOME/walky).
func Dir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "walky"), nil
}

// IndexPath returns the hash-cache path for a repository. The name is derived
// from the absolute repository path so several repositories can coexist.
func IndexPath(repo string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(dir, "index-"+hex.EncodeToString(sum[:8])+".db"), nil
}
