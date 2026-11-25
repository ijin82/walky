package daemon

import (
	"errors"
	"path/filepath"
	"strings"

	"walky/internal/store"
)

// ListSnapshots returns committed generations on the attached drive, newest first.
func (d *Daemon) ListSnapshots() ([]SnapshotSummary, error) {
	d.mu.Lock()
	dev := d.dev
	d.mu.Unlock()
	if dev == nil {
		return nil, ErrNoDevice
	}
	st, err := store.Open(dev.StoreRoot)
	if err != nil {
		return nil, err
	}
	snaps, err := st.Snapshots()
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotSummary, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, SnapshotSummary{
			Gen:     s.Gen,
			Created: s.Created.Unix(),
			Files:   int64(s.Files),
			Bytes:   s.Bytes,
		})
	}
	return out, nil
}

// ListSnapshotFiles returns files and symlinks in generation gen matching query
// (case-insensitive substring), capped at limit (default 200).
func (d *Daemon) ListSnapshotFiles(gen int64, query string, limit int) ([]SnapshotFile, error) {
	d.mu.Lock()
	dev := d.dev
	d.mu.Unlock()
	if dev == nil {
		return nil, ErrNoDevice
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	st, err := store.Open(dev.StoreRoot)
	if err != nil {
		return nil, err
	}
	var m *store.Manifest
	if gen <= 0 {
		m, _, err = st.Latest()
	} else {
		m, _, err = st.ReadSnapshot(gen)
	}
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]SnapshotFile, 0, min(limit, len(m.Entries)))
	for _, e := range m.Entries {
		if e.Type != store.TypeFile && e.Type != store.TypeSymlink {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Path), q) {
			continue
		}
		out = append(out, SnapshotFile{
			Path:  e.Path,
			Type:  string(e.Type),
			Size:  e.Size,
			Mtime: e.MtimeNS / 1e9,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RestoreFile restores one file or directory path from generation gen into
// destDir. When destDir is empty, it restores into the local sync folder.
func (d *Daemon) RestoreFile(gen int64, relPath, destDir string) (string, error) {
	d.mu.Lock()
	dev := d.dev
	repo := d.cfg.RepoPath
	syncing := d.syncing
	d.mu.Unlock()
	if dev == nil {
		return "", ErrNoDevice
	}
	if syncing {
		return "", ErrBusy
	}
	dest := strings.TrimSpace(destDir)
	if dest == "" {
		if repo == "" {
			return "", errors.New("sync folder is not configured")
		}
		dest = repo
	}
	st, err := store.Open(dev.StoreRoot)
	if err != nil {
		return "", err
	}
	var m *store.Manifest
	if gen <= 0 {
		m, _, err = st.Latest()
	} else {
		m, _, err = st.ReadSnapshot(gen)
	}
	if err != nil {
		return "", err
	}
	if err := st.RestorePath(dest, m, relPath, true); err != nil {
		return "", err
	}
	restored := filepath.Join(dest, filepath.FromSlash(relPath))
	d.log.Info("restored path from history", "gen", gen, "path", relPath, "dest", restored)
	return restored, nil
}
