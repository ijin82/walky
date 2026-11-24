package store

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zeebo/blake3"
)

// Restore materializes a snapshot into dest. The destination must be empty
// unless force is set. Every object is re-hashed while copying, so a corrupt
// drive is detected instead of silently producing broken files.
func (s *Store) Restore(dest string, m *Manifest, force bool) error {
	if err := m.Normalize(); err != nil {
		return err
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	existing, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	if len(existing) > 0 && !force {
		return fmt.Errorf("destination %s is not empty (use --force)", abs)
	}

	root, err := os.OpenRoot(abs)
	if err != nil {
		return err
	}
	defer root.Close()

	var dirs []Entry
	for _, e := range m.Entries {
		p := filepath.FromSlash(e.Path)
		switch e.Type {
		case TypeDir:
			if err := root.MkdirAll(p, UnixToMode(e.Mode)|0o700); err != nil {
				return fmt.Errorf("create directory %q: %w", e.Path, err)
			}
			dirs = append(dirs, e)
		case TypeFile:
			if dir := filepath.Dir(p); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("create parent of %q: %w", e.Path, err)
				}
			}
			if err := s.restoreFile(root, p, e, force); err != nil {
				return err
			}
		case TypeSymlink:
			if dir := filepath.Dir(p); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("create parent of %q: %w", e.Path, err)
				}
			}
			if force {
				if err := root.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("replace %q: %w", e.Path, err)
				}
			}
			if err := root.Symlink(e.Target, p); err != nil {
				return fmt.Errorf("create symlink %q: %w", e.Path, err)
			}
		case TypeTombstone:
			// Deletions are not restored.
		}
	}

	// Directory metadata goes last, children before parents, so writes do not
	// disturb the restored timestamps.
	for i := len(dirs) - 1; i >= 0; i-- {
		e := dirs[i]
		p := filepath.FromSlash(e.Path)
		if e.Mode != 0 {
			if err := root.Chmod(p, UnixToMode(e.Mode)); err != nil {
				return fmt.Errorf("chmod directory %q: %w", e.Path, err)
			}
		}
		if e.MtimeNS > 0 {
			t := time.Unix(0, e.MtimeNS)
			if err := root.Chtimes(p, t, t); err != nil {
				return fmt.Errorf("chtimes directory %q: %w", e.Path, err)
			}
		}
	}
	return syncPath(abs)
}

func (s *Store) restoreFile(root *os.Root, p string, e Entry, force bool) error {
	if force {
		if err := root.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("replace %q: %w", e.Path, err)
		}
	} else if _, err := root.Lstat(p); err == nil {
		return fmt.Errorf("%q already exists (use --force)", e.Path)
	}

	f, err := root.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %q: %w", e.Path, err)
	}
	obj, err := s.OpenObject(e.Hash)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("open object for %q: %w", e.Path, err)
	}
	defer obj.Close()

	h := blake3.New()
	n, err := io.Copy(io.MultiWriter(f, h), obj)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("copy %q: %w", e.Path, err)
	}
	if n != e.Size {
		_ = f.Close()
		return fmt.Errorf("%q: size mismatch: manifest %d, object %d", e.Path, e.Size, n)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.Hash {
		_ = f.Close()
		return fmt.Errorf("%q: object %s is corrupt (hash %s)", e.Path, e.Hash, got)
	}
	if err := f.Chmod(UnixToMode(e.Mode)); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod %q: %w", e.Path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", e.Path, err)
	}
	if e.MtimeNS > 0 {
		t := time.Unix(0, e.MtimeNS)
		if err := root.Chtimes(p, t, t); err != nil {
			return fmt.Errorf("chtimes %q: %w", e.Path, err)
		}
	}
	return nil
}
