package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WriteManifestFile encodes a manifest and writes it atomically. It is used for
// local base manifests outside the drive.
func WriteManifestFile(path string, m *Manifest) error {
	if err := m.Normalize(); err != nil {
		return err
	}
	data, err := EncodeManifest(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

// ReadManifestFile reads a manifest written by WriteManifestFile.
func ReadManifestFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeManifest(data)
}

// Machine is the per-machine state stored on the drive.
type Machine struct {
	ID       string    `json:"id"`
	Hostname string    `json:"hostname,omitempty"`
	BaseGen  int64     `json:"base_gen"`
	LastSync time.Time `json:"last_sync"`
}

// SaveMachine writes machines/<id>.json atomically.
func (s *Store) SaveMachine(m Machine) error {
	if m.ID == "" {
		return errors.New("machine id is empty")
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(s.root, machinesDir, m.ID+".json")
	return writeFileAtomic(path, data, 0o644)
}

// LoadMachine reads machines/<id>.json.
func (s *Store) LoadMachine(id string) (Machine, error) {
	var m Machine
	data, err := os.ReadFile(filepath.Join(s.root, machinesDir, id+".json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse machine %s: %w", id, err)
	}
	return m, nil
}

// SaveTrash records the entries deleted by a generation so retention can keep
// their content reachable.
func (s *Store) SaveTrash(gen int64, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(s.root, trashDir, fmt.Sprintf("%08d.json", gen))
	return writeFileAtomic(path, data, 0o644)
}

// TrashRecord is one generation's deletion list.
type TrashRecord struct {
	Gen     int64
	Entries []Entry
	Path    string
}

// TrashRecords lists the deletion records stored on the drive.
func (s *Store) TrashRecords() ([]TrashRecord, error) {
	dir := filepath.Join(s.root, trashDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []TrashRecord
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var list []Entry
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("parse trash %s: %w", de.Name(), err)
		}
		var gen int64
		if _, err := fmt.Sscanf(de.Name(), "%d.json", &gen); err != nil {
			continue
		}
		out = append(out, TrashRecord{Gen: gen, Entries: list, Path: path})
	}
	return out, nil
}

// ClearTrash removes every deletion record and returns the count and bytes.
func (s *Store) ClearTrash() (int, int64, error) {
	records, err := s.TrashRecords()
	if err != nil {
		return 0, 0, err
	}
	var removed int
	var bytes int64
	for _, rec := range records {
		if fi, err := os.Stat(rec.Path); err == nil {
			bytes += fi.Size()
		}
		if err := os.Remove(rec.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, bytes, err
		}
		removed++
	}
	return removed, bytes, nil
}
