// Package machineid provides a stable identifier for the local machine, used
// to track per-machine sync state on the drive.
package machineid

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Path returns the location of the local machine id file.
func Path() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "walky", "machine-id"), nil
}

// Get returns the machine id, creating it on first use.
func Get() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	if data, err := os.ReadFile(p); err == nil {
		if id := strings.TrimSpace(string(data)); len(id) == 32 {
			return id, nil
		}
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
