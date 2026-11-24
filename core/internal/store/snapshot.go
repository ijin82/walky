package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Head is the single atomic pointer to the current generation.
type Head struct {
	Format         int    `json:"format"`
	Gen            int64  `json:"gen"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// Meta describes one committed generation.
type Meta struct {
	Format         int       `json:"format"`
	Gen            int64     `json:"gen"`
	Parent         int64     `json:"parent,omitempty"`
	Machine        string    `json:"machine,omitempty"`
	Message        string    `json:"message,omitempty"`
	Created        time.Time `json:"created"`
	Entries        int       `json:"entries"`
	Files          int       `json:"files"`
	Bytes          int64     `json:"bytes"`
	ManifestSHA256 string    `json:"manifest_sha256"`
}

// CommitOptions carries metadata for a new generation.
type CommitOptions struct {
	Machine string
	Message string
}

// CommitResult reports the committed generation.
type CommitResult struct {
	Gen     int64
	Changed bool
}

// testHook, when set by tests, runs before each durable commit step and can
// abort it, simulating a crash or an I/O error.
var testHook func(step string) error

func stepHook(step string) error {
	if testHook != nil {
		return testHook(step)
	}
	return nil
}

// Head reads the current generation pointer.
func (s *Store) Head() (Head, error) {
	data, err := os.ReadFile(filepath.Join(s.root, headName))
	if errors.Is(err, os.ErrNotExist) {
		return Head{}, ErrNoHead
	}
	if err != nil {
		return Head{}, err
	}
	var h Head
	if err := json.Unmarshal(data, &h); err != nil {
		return Head{}, fmt.Errorf("parse %s: %w", headName, err)
	}
	if h.Format != FormatVersion || h.Gen <= 0 {
		return Head{}, fmt.Errorf("invalid %s: %+v", headName, h)
	}
	return h, nil
}

// Commit writes the manifest and meta files, flushes the drive and moves HEAD.
// Objects referenced by the manifest must already exist. Committing an
// unchanged tree is a no-op.
func (s *Store) Commit(m *Manifest, opts CommitOptions) (CommitResult, error) {
	unlock, err := s.Lock()
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { _ = unlock() }()
	return s.commitLocked(m, opts)
}

func (s *Store) commitLocked(m *Manifest, opts CommitOptions) (CommitResult, error) {
	if err := m.Normalize(); err != nil {
		return CommitResult{}, err
	}
	data, err := EncodeManifest(m)
	if err != nil {
		return CommitResult{}, err
	}
	sum := sha256.Sum256(data)
	sumHex := hex.EncodeToString(sum[:])

	var parent int64
	head, err := s.Head()
	switch {
	case err == nil:
		if head.ManifestSHA256 == sumHex {
			return CommitResult{Gen: head.Gen, Changed: false}, nil
		}
		parent = head.Gen
	case errors.Is(err, ErrNoHead):
		parent = 0
	default:
		return CommitResult{}, err
	}

	// Every referenced object must be on the drive before HEAD can point at it.
	for _, e := range m.Entries {
		if e.Type != TypeFile {
			continue
		}
		ok, err := s.HasObject(e.Hash)
		if err != nil {
			return CommitResult{}, err
		}
		if !ok {
			return CommitResult{}, fmt.Errorf("object %s for %q is missing", e.Hash, e.Path)
		}
	}

	if err := stepHook("sync"); err != nil {
		return CommitResult{}, err
	}
	if err := s.Sync(); err != nil {
		return CommitResult{}, err
	}

	gen := parent + 1
	entries, files, bytesTotal := m.Stats()
	meta := Meta{
		Format:         FormatVersion,
		Gen:            gen,
		Parent:         parent,
		Machine:        opts.Machine,
		Message:        opts.Message,
		Created:        time.Now().UTC(),
		Entries:        entries,
		Files:          files,
		Bytes:          bytesTotal,
		ManifestSHA256: sumHex,
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return CommitResult{}, err
	}
	metaData = append(metaData, '\n')

	if err := stepHook("snapshot"); err != nil {
		return CommitResult{}, err
	}
	if err := writeFileAtomic(s.snapshotPath(gen), data, 0o644); err != nil {
		return CommitResult{}, fmt.Errorf("write snapshot %d: %w", gen, err)
	}

	if err := stepHook("meta"); err != nil {
		return CommitResult{}, err
	}
	if err := writeFileAtomic(s.metaPath(gen), metaData, 0o644); err != nil {
		return CommitResult{}, fmt.Errorf("write meta %d: %w", gen, err)
	}

	headData, err := json.Marshal(Head{
		Format:         FormatVersion,
		Gen:            gen,
		ManifestSHA256: sumHex,
	})
	if err != nil {
		return CommitResult{}, err
	}
	headData = append(headData, '\n')

	if err := stepHook("head"); err != nil {
		return CommitResult{}, err
	}
	if err := writeFileAtomic(filepath.Join(s.root, headName), headData, 0o644); err != nil {
		return CommitResult{}, fmt.Errorf("write %s: %w", headName, err)
	}
	return CommitResult{Gen: gen, Changed: true}, nil
}

func (s *Store) snapshotPath(gen int64) string {
	return filepath.Join(s.root, snapshotsDir, fmt.Sprintf("%08d.jsonl.zst", gen))
}

func (s *Store) metaPath(gen int64) string {
	return filepath.Join(s.root, snapshotsDir, fmt.Sprintf("%08d.meta.json", gen))
}

// ReadSnapshot loads one generation and checks it against its metadata.
func (s *Store) ReadSnapshot(gen int64) (*Manifest, *Meta, error) {
	metaData, err := os.ReadFile(s.metaPath(gen))
	if err != nil {
		return nil, nil, fmt.Errorf("read meta %d: %w", gen, err)
	}
	var meta Meta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, nil, fmt.Errorf("parse meta %d: %w", gen, err)
	}
	if meta.Format != FormatVersion || meta.Gen != gen {
		return nil, nil, fmt.Errorf("invalid meta for generation %d", gen)
	}
	data, err := os.ReadFile(s.snapshotPath(gen))
	if err != nil {
		return nil, nil, fmt.Errorf("read snapshot %d: %w", gen, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != meta.ManifestSHA256 {
		return nil, nil, fmt.Errorf("snapshot %d checksum mismatch", gen)
	}
	m, err := DecodeManifest(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode snapshot %d: %w", gen, err)
	}
	return m, &meta, nil
}

// Latest loads the generation HEAD points at.
func (s *Store) Latest() (*Manifest, *Meta, error) {
	head, err := s.Head()
	if err != nil {
		return nil, nil, err
	}
	return s.ReadSnapshot(head.Gen)
}
