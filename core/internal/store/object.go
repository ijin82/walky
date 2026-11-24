package store

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/blake3"
)

// ObjectPath returns the standalone on-drive path of an object by its blake3
// hex digest. Packed objects do not have their own path.
func (s *Store) ObjectPath(hash string) (string, error) {
	if !validHash(hash) {
		return "", fmt.Errorf("invalid object hash %q", hash)
	}
	return filepath.Join(s.root, objectsDir, hash[0:2], hash[2:4], hash), nil
}

// HasObject reports whether the object is stored, standalone or packed.
func (s *Store) HasObject(hash string) (bool, error) {
	if _, err := s.ObjectInfo(hash); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ObjectInfo returns the stored object size.
func (s *Store) ObjectInfo(hash string) (int64, error) {
	p, err := s.ObjectPath(hash)
	if err != nil {
		return 0, err
	}
	if fi, err := os.Stat(p); err == nil {
		return fi.Size(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	h, err := decodeHash(hash)
	if err != nil {
		return 0, err
	}
	cat, err := s.catalog()
	if err != nil {
		return 0, err
	}
	if loc, ok := cat.lookup(h); ok {
		return int64(loc.rawLen), nil
	}
	return 0, os.ErrNotExist
}

// PutObject stores the reader contents as a standalone object and returns the
// blake3 digest and size. Durability is provided by Store.Sync in the commit
// protocol, not by an fsync per object.
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
	// Always rename over an existing file: an object left by an interrupted
	// commit may be truncated, so callers decide deduplication from the set of
	// objects referenced by HEAD (see Store.CommittedHashes).
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, err
	}
	tmp = ""
	return hash, n, nil
}

// OpenObject opens an object for reading, whether it is standalone or packed.
func (s *Store) OpenObject(hash string) (io.ReadCloser, error) {
	p, err := s.ObjectPath(hash)
	if err != nil {
		return nil, err
	}
	if f, err := os.Open(p); err == nil {
		return f, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	h, err := decodeHash(hash)
	if err != nil {
		return nil, err
	}
	cat, err := s.catalog()
	if err != nil {
		return nil, err
	}
	loc, ok := cat.lookup(h)
	if !ok {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.root, packsDir, cat.packs[loc.pack]))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	zr, err := zstd.NewReader(io.NewSectionReader(f, loc.off, int64(loc.compLen)))
	if err != nil {
		return nil, fmt.Errorf("open packed object %s: %w", hash, err)
	}
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, int64(loc.rawLen)+1))
	if err != nil {
		return nil, fmt.Errorf("read packed object %s: %w", hash, err)
	}
	if len(data) != int(loc.rawLen) {
		return nil, fmt.Errorf("packed object %s: size mismatch (%d != %d)", hash, len(data), loc.rawLen)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
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
