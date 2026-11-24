// Package store implements the content-addressed, append-only snapshot storage
// that lives on the Walky drive.
//
// On-drive layout:
//
//	walky.json                  marker: format, repo_id, created
//	HEAD                        {"format":1,"gen":N,"manifest_sha256":"..."}
//	objects/ab/cd/<blake3>      file contents
//	packs/pack-<id>.pack        packed small objects (one zstd frame each)
//	packs/pack-<id>.idx         binary index: hash, offset, compressed/raw size
//	snapshots/NNNNNNNN.jsonl.zst  full manifest of one generation
//	snapshots/NNNNNNNN.meta.json  generation metadata
//	machines/<id>.json          per-machine base (M4)
//	trash/                      deleted paths retention (M5)
//	log/                        rotated logs
//	lock                        flock for the single writer
//
// The commit protocol guarantees that HEAD is written last: an interrupted
// commit leaves unreferenced objects behind, never a partially visible
// snapshot.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// MarkerName identifies a Walky drive.
	MarkerName = "walky.json"
	// FormatVersion is the on-drive format understood by this build.
	FormatVersion = 1

	headName     = "HEAD"
	objectsDir   = "objects"
	packsDir     = "packs"
	snapshotsDir = "snapshots"
	machinesDir  = "machines"
	trashDir     = "trash"
	logDir       = "log"
	lockName     = "lock"
)

var (
	// ErrNotInitialized means the directory has no walky.json marker.
	ErrNotInitialized = errors.New("not a walky store")
	// ErrAlreadyInitialized means the directory already holds a store.
	ErrAlreadyInitialized = errors.New("store is already initialized")
	// ErrNotEmpty means Init refused to adopt a non-empty directory.
	ErrNotEmpty = errors.New("directory is not empty")
	// ErrNoHead means the store has no committed snapshots yet.
	ErrNoHead = errors.New("store has no snapshots")
	// ErrHeadChanged means another writer advanced HEAD during this sync.
	ErrHeadChanged = errors.New("store HEAD changed during sync")
	// ErrLocked means another process holds the store lock.
	ErrLocked = errors.New("store is locked by another process")
)

// Marker is the content of walky.json.
type Marker struct {
	Format  int       `json:"format"`
	RepoID  string    `json:"repo_id"`
	Created time.Time `json:"created"`
	Label   string    `json:"label,omitempty"`
}

// Store is an opened Walky storage directory.
type Store struct {
	root   string
	marker Marker

	catMu sync.Mutex
	cat   *packCatalog
}

// Init creates a new store in root. When force is false, a non-empty
// directory is refused so that a wrong path cannot be adopted silently.
// Formatting or mounting the device is the caller's job.
func Init(root string, force bool) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, fmt.Errorf("create store root: %w", err)
		}
		fi, err = os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("store root: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("store root: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("store root %s is not a directory", abs)
	}

	markerPath := filepath.Join(abs, MarkerName)
	switch _, err := os.Stat(markerPath); {
	case err == nil:
		return nil, fmt.Errorf("%w: %s", ErrAlreadyInitialized, markerPath)
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 && !force {
		return nil, fmt.Errorf("%w: %s (use --force to adopt it)", ErrNotEmpty, abs)
	}

	for _, d := range []string{objectsDir, packsDir, snapshotsDir, machinesDir, trashDir, logDir} {
		if err := os.MkdirAll(filepath.Join(abs, d), 0o755); err != nil {
			return nil, err
		}
	}

	repoID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	marker := Marker{Format: FormatVersion, RepoID: repoID, Created: time.Now().UTC()}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if err := writeFileAtomic(markerPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write marker: %w", err)
	}
	if err := syncDir(abs); err != nil {
		return nil, err
	}
	return Open(abs)
}

// ReadMarker reads and validates the marker without touching the directory.
// It is safe to call on foreign mounted filesystems.
func ReadMarker(root string) (Marker, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Marker{}, err
	}
	data, err := os.ReadFile(filepath.Join(abs, MarkerName))
	if errors.Is(err, os.ErrNotExist) {
		return Marker{}, fmt.Errorf("%w: %s", ErrNotInitialized, abs)
	}
	if err != nil {
		return Marker{}, err
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return Marker{}, fmt.Errorf("parse %s: %w", MarkerName, err)
	}
	if m.Format != FormatVersion {
		return Marker{}, fmt.Errorf("unsupported store format %d (this build supports %d)",
			m.Format, FormatVersion)
	}
	if len(m.RepoID) != 32 {
		return Marker{}, fmt.Errorf("invalid repo_id %q in %s", m.RepoID, MarkerName)
	}
	if _, err := hex.DecodeString(m.RepoID); err != nil {
		return Marker{}, fmt.Errorf("invalid repo_id %q: %w", m.RepoID, err)
	}
	return m, nil
}

// Open reads the marker and validates the on-drive format. Missing layout
// directories are recreated, which makes a store usable after a partial copy.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	m, err := ReadMarker(abs)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{objectsDir, packsDir, snapshotsDir, machinesDir, trashDir, logDir} {
		if err := os.MkdirAll(filepath.Join(abs, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{root: abs, marker: m}, nil
}

// Root returns the absolute store path.
func (s *Store) Root() string { return s.root }

// Marker returns a copy of the drive marker.
func (s *Store) Marker() Marker { return s.marker }

// Lock takes the exclusive writer lock. The returned function releases it.
func (s *Store) Lock() (func() error, error) {
	f, err := os.OpenFile(filepath.Join(s.root, lockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() error {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		return f.Close()
	}, nil
}

// Sync flushes the whole store filesystem to the device. It is called once
// per commit, after all objects are written and before HEAD moves, instead of
// fsyncing every object (which is prohibitively slow for many small files).
func (s *Store) Sync() error {
	f, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Syncfs(int(f.Fd())); err != nil {
		return fmt.Errorf("syncfs %s: %w", s.root, err)
	}
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeFileAtomic writes data via a temporary file, fsyncs it, renames it into
// place and fsyncs the parent directory.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	tmp = "" // ownership transferred to path
	return syncDir(dir)
}

// syncDir flushes directory entries (renames, creates) to disk.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync dir %s: %w", path, err)
	}
	return nil
}

// syncPath flushes everything on the filesystem that contains path.
func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Syncfs(int(f.Fd())); err != nil {
		return fmt.Errorf("syncfs %s: %w", path, err)
	}
	return nil
}
