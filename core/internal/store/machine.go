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
