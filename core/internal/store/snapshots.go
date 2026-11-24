package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SnapshotInfo describes one committed generation.
type SnapshotInfo struct {
	Gen     int64
	Created time.Time
	Entries int
	Files   int
	Bytes   int64
}

// Snapshots lists committed generations, newest first.
func (s *Store) Snapshots() ([]SnapshotInfo, error) {
	metas, err := s.snapshotMetas()
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotInfo, 0, len(metas))
	for _, m := range metas {
		out = append(out, SnapshotInfo{
			Gen: m.Gen, Created: m.Created,
			Entries: m.Entries, Files: m.Files, Bytes: m.Bytes,
		})
	}
	return out, nil
}

// snapshotMetas reads every generation metadata file, newest first.
func (s *Store) snapshotMetas() ([]Meta, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, snapshotsDir))
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, snapshotsDir, name))
		if err != nil {
			return nil, err
		}
		var meta Meta
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Gen > out[j].Gen })
	return out, nil
}
