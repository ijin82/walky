package store

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zeebo/blake3"
)

// ObjectPath returns the on-drive path of an object by its blake3 hex digest.
func (s *Store) ObjectPath(hash string) (string, error) {
	if !validHash(hash) {
		return "", fmt.Errorf("invalid object hash %q", hash)
	}
	return filepath.Join(s.root, objectsDir, hash[0:2], hash[2:4], hash), nil
}

// HasObject reports whether the object is already stored.
func (s *Store) HasObject(hash string) (bool, error) {
	p, err := s.ObjectPath(hash)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	return false, nil
}

// PutObject stores the reader contents and returns the blake3 digest and size.
// The object is written to a temporary file first, so a failed call never
// leaves a truncated object under its final name. Durability is provided by
// Store.Sync in the commit protocol, not by an fsync per object.
func (s *Store) PutObject(r io.Reader) (string, int64, error) {
	f, err := os.CreateTemp(filepath.Join(s.root, objectsDir), ".tmp-obj-")
	if err != nil {
		return "", 0, err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()

	h := blake3.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		_ = f.Close()
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}

	hash := hex.EncodeToString(h.Sum(nil))
	dst, err := s.ObjectPath(hash)
	if err != nil {
		return "", 0, err
	}
	if _, err := os.Stat(dst); err == nil {
		return hash, n, nil // deduplicated
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, err
	}
	tmp = ""
	return hash, n, nil
}

// OpenObject opens an object for reading.
func (s *Store) OpenObject(hash string) (*os.File, error) {
	p, err := s.ObjectPath(hash)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
