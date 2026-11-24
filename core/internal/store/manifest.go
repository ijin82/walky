package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// EntryType is the kind of a manifest entry.
type EntryType string

const (
	// TypeFile is a regular file backed by an object.
	TypeFile EntryType = "f"
	// TypeDir is a directory.
	TypeDir EntryType = "d"
	// TypeSymlink is a symbolic link.
	TypeSymlink EntryType = "l"
	// TypeTombstone marks a deletion (used from M4 on).
	TypeTombstone EntryType = "x"
)

// Entry describes one path in a snapshot. Paths always use forward slashes and
// are relative to the sync root.
type Entry struct {
	Path    string    `json:"p"`
	Type    EntryType `json:"t"`
	Size    int64     `json:"s,omitempty"`
	Mode    uint32    `json:"m,omitempty"`
	MtimeNS int64     `json:"mt,omitempty"`
	Hash    string    `json:"h,omitempty"`
	Target  string    `json:"tg,omitempty"`
}

// Manifest is a full snapshot: every path of the tree, sorted by path.
type Manifest struct {
	Entries []Entry
}

// Normalize sorts entries and validates the result.
func (m *Manifest) Normalize() error {
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	return m.Validate()
}

// Validate checks entry invariants without reordering.
func (m *Manifest) Validate() error {
	prev := ""
	for i, e := range m.Entries {
		if err := validateEntry(e); err != nil {
			return err
		}
		if i > 0 {
			if e.Path == prev {
				return fmt.Errorf("duplicate path %q", e.Path)
			}
			if e.Path < prev {
				return fmt.Errorf("manifest is not sorted: %q before %q", e.Path, prev)
			}
		}
		prev = e.Path
	}
	return nil
}

func validateEntry(e Entry) error {
	if !filepath.IsLocal(filepath.FromSlash(e.Path)) || e.Path == "." {
		return fmt.Errorf("unsafe path %q", e.Path)
	}
	if strings.Contains(e.Path, "\\") {
		return fmt.Errorf("path %q contains a backslash", e.Path)
	}
	if e.Mode&^0o7777 != 0 {
		return fmt.Errorf("path %q has invalid mode %#o", e.Path, e.Mode)
	}
	if e.Size < 0 {
		return fmt.Errorf("path %q has negative size", e.Path)
	}
	switch e.Type {
	case TypeFile:
		if !validHash(e.Hash) {
			return fmt.Errorf("file %q has invalid hash %q", e.Path, e.Hash)
		}
	case TypeDir:
		if e.Hash != "" || e.Target != "" || e.Size != 0 {
			return fmt.Errorf("directory %q carries file fields", e.Path)
		}
	case TypeSymlink:
		if e.Target == "" {
			return fmt.Errorf("symlink %q has empty target", e.Path)
		}
		if e.Hash != "" {
			return fmt.Errorf("symlink %q carries a hash", e.Path)
		}
	case TypeTombstone:
		// Deletion markers carry no content.
	default:
		return fmt.Errorf("path %q has unknown type %q", e.Path, e.Type)
	}
	return nil
}

// Find returns the entry for path.
func (m *Manifest) Find(path string) (Entry, bool) {
	i := sort.Search(len(m.Entries), func(i int) bool { return m.Entries[i].Path >= path })
	if i < len(m.Entries) && m.Entries[i].Path == path {
		return m.Entries[i], true
	}
	return Entry{}, false
}

// Stats returns entry count, file count and total file bytes.
func (m *Manifest) Stats() (entries, files int, bytes int64) {
	for _, e := range m.Entries {
		entries++
		if e.Type == TypeFile {
			files++
			bytes += e.Size
		}
	}
	return entries, files, bytes
}

// EncodeManifest serializes the manifest as zstd-compressed JSON lines.
// The encoding is deterministic: the same entries always produce the same
// bytes, so the digest can be compared to skip no-op commits.
func EncodeManifest(m *Manifest) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	for _, e := range m.Entries {
		if err := enc.Encode(e); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeManifest reads a manifest produced by EncodeManifest.
func DecodeManifest(data []byte) (*Manifest, error) {
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open zstd stream: %w", err)
	}
	defer zr.Close()

	m := &Manifest{}
	dec := json.NewDecoder(zr)
	for {
		var e Entry
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode entry %d: %w", len(m.Entries)+1, err)
		}
		m.Entries = append(m.Entries, e)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// ModeToUnix maps fs.FileMode permission and special bits to the Unix mode
// bits stored in a manifest.
func ModeToUnix(m fs.FileMode) uint32 {
	u := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		u |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		u |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		u |= 0o1000
	}
	return u
}

// UnixToMode is the inverse of ModeToUnix.
func UnixToMode(u uint32) fs.FileMode {
	m := fs.FileMode(u & 0o777)
	if u&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if u&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if u&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}
