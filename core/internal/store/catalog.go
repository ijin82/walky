package store

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	packMagic     = "WPK1"
	packEntrySize = 48 // hash[32] + off[8] + compLen[4] + rawLen[4]
)

type packLoc struct {
	hash    [32]byte
	pack    int32
	off     int64
	compLen uint32
	rawLen  uint32
}

// packCatalog maps object hashes to their location inside pack files.
// It is loaded lazily and cached until a new pack is written.
type packCatalog struct {
	packs []string
	locs  []packLoc
}

func (c *packCatalog) lookup(h [32]byte) (packLoc, bool) {
	i := sort.Search(len(c.locs), func(i int) bool {
		return bytes.Compare(c.locs[i].hash[:], h[:]) >= 0
	})
	if i < len(c.locs) && c.locs[i].hash == h {
		return c.locs[i], true
	}
	return packLoc{}, false
}

func (c *packCatalog) len() int { return len(c.locs) }

// catalog loads the pack catalog on first use.
func (s *Store) catalog() (*packCatalog, error) {
	s.catMu.Lock()
	defer s.catMu.Unlock()
	if s.cat != nil {
		return s.cat, nil
	}
	cat, err := loadCatalog(s.root)
	if err != nil {
		return nil, err
	}
	s.cat = cat
	return cat, nil
}

func (s *Store) invalidateCatalog() {
	s.catMu.Lock()
	s.cat = nil
	s.catMu.Unlock()
}

func loadCatalog(root string) (*packCatalog, error) {
	dir := filepath.Join(root, packsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &packCatalog{}, nil
		}
		return nil, err
	}
	cat := &packCatalog{}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || filepath.Ext(name) != ".idx" || strings.HasPrefix(name, ".") {
			continue
		}
		locs, err := readPackIndex(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read pack index %s: %w", name, err)
		}
		pack := int32(len(cat.packs))
		cat.packs = append(cat.packs, strings.TrimSuffix(name, ".idx")+".pack")
		for i := range locs {
			locs[i].pack = pack
		}
		cat.locs = append(cat.locs, locs...)
	}
	sort.Slice(cat.locs, func(i, j int) bool {
		return bytes.Compare(cat.locs[i].hash[:], cat.locs[j].hash[:]) < 0
	})
	return cat, nil
}

func readPackIndex(path string) ([]packLoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePackIndex(data)
}

func parsePackIndex(data []byte) ([]packLoc, error) {
	if len(data) < 8 || string(data[:4]) != packMagic {
		return nil, errors.New("bad pack index magic")
	}
	count := int(binary.LittleEndian.Uint32(data[4:8]))
	if len(data) != 8+count*packEntrySize {
		return nil, fmt.Errorf("bad pack index size: %d entries in %d bytes", count, len(data))
	}
	locs := make([]packLoc, count)
	for i := 0; i < count; i++ {
		p := 8 + i*packEntrySize
		copy(locs[i].hash[:], data[p:p+32])
		locs[i].off = int64(binary.LittleEndian.Uint64(data[p+32 : p+40]))
		locs[i].compLen = binary.LittleEndian.Uint32(data[p+40 : p+44])
		locs[i].rawLen = binary.LittleEndian.Uint32(data[p+44 : p+48])
	}
	return locs, nil
}

func encodePackIndex(entries []packEntry) ([]byte, error) {
	sorted := append([]packEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].hash[:], sorted[j].hash[:]) < 0
	})
	buf := bytes.NewBuffer(make([]byte, 0, 8+len(sorted)*packEntrySize))
	buf.WriteString(packMagic)
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(sorted))); err != nil {
		return nil, err
	}
	for _, e := range sorted {
		buf.Write(e.hash[:])
		if err := binary.Write(buf, binary.LittleEndian, e.off); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, e.compLen); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, e.rawLen); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeHash(h string) ([32]byte, error) {
	var out [32]byte
	if !validHash(h) {
		return out, fmt.Errorf("invalid object hash %q", h)
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}
