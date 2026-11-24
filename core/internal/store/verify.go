package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// VerifyReport summarizes an integrity check.
type VerifyReport struct {
	Gen          int64
	Entries      int
	Objects      int
	Bytes        int64
	Missing      []string // paths whose object is absent
	Corrupt      []string // paths whose object hash does not match
	SizeMismatch []string // paths whose object size differs from the manifest
}

// OK reports whether the store passed the check.
func (r *VerifyReport) OK() bool {
	return len(r.Missing) == 0 && len(r.Corrupt) == 0 && len(r.SizeMismatch) == 0
}

// Verify checks HEAD, the manifest digest and the objects. A quick check only
// stats objects; a full check re-hashes every object.
func (s *Store) Verify(full bool) (*VerifyReport, error) {
	head, err := s.Head()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.snapshotPath(head.Gen))
	if err != nil {
		return nil, fmt.Errorf("read snapshot %d: %w", head.Gen, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != head.ManifestSHA256 {
		return nil, fmt.Errorf("snapshot %d does not match HEAD", head.Gen)
	}
	m, err := DecodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("decode snapshot %d: %w", head.Gen, err)
	}

	report := &VerifyReport{Gen: head.Gen, Entries: len(m.Entries)}
	for _, e := range m.Entries {
		if e.Type != TypeFile {
			continue
		}
		report.Objects++
		report.Bytes += e.Size

		size, err := s.ObjectInfo(e.Hash)
		if err != nil {
			if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
				report.Missing = append(report.Missing, e.Path)
			} else {
				report.Corrupt = append(report.Corrupt, e.Path)
			}
			continue
		}
		if size != e.Size {
			report.SizeMismatch = append(report.SizeMismatch, e.Path)
			continue
		}
		if !full {
			continue
		}
		if err := s.CheckObject(e.Hash); err != nil {
			report.Corrupt = append(report.Corrupt, e.Path)
		}
	}
	return report, nil
}

// CheckObject reports whether a single object matches its digest.
func (s *Store) CheckObject(hash string) error {
	rc, err := s.OpenObject(hash)
	if err != nil {
		return err
	}
	defer rc.Close()
	h := blake3.New()
	if _, err := io.Copy(h, rc); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != hash {
		return fmt.Errorf("hash mismatch: got %s, want %s", got, hash)
	}
	return nil
}
