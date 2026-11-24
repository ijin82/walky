package store

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/blake3"
)

const (
	// PackThreshold is the size from which an object is stored as a standalone
	// file instead of being packed.
	PackThreshold int64 = 1 << 20
	// PackTargetSize is the raw size at which a pack is closed.
	PackTargetSize int64 = 32 << 20
)

// PackOptions tune pack creation.
type PackOptions struct {
	Threshold  int64
	TargetSize int64
}

// DefaultPackOptions returns the production thresholds.
func DefaultPackOptions() PackOptions {
	return PackOptions{Threshold: PackThreshold, TargetSize: PackTargetSize}
}

type packEntry struct {
	hash    [32]byte
	off     int64
	compLen uint32
	rawLen  uint32
}

// Packer groups small objects into pack files. Objects are compressed one by
// one, so a pack can be read randomly without decompressing its neighbours.
// Call Close before committing the manifest.
type Packer struct {
	s       *Store
	opt     PackOptions
	f       *os.File
	tmp     string
	size    int64
	entries []packEntry
	zw      *zstd.Encoder
	packs   int
	closed  bool
}

// NewPacker creates a pack writer for the store.
func (s *Store) NewPacker(opt PackOptions) *Packer {
	if opt.TargetSize <= 0 {
		opt.TargetSize = PackTargetSize
	}
	if opt.Threshold <= 0 {
		opt.Threshold = PackThreshold
	}
	zw, _ := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(zstd.SpeedDefault))
	return &Packer{s: s, opt: opt, zw: zw}
}

// Threshold reports the size from which objects must be stored standalone.
func (p *Packer) Threshold() int64 { return p.opt.Threshold }

// Packs returns the number of completed packs.
func (p *Packer) Packs() int { return p.packs }

// Add reads r, verifies it against hash and size, and appends it to the
// current pack. The reader is consumed entirely.
func (p *Packer) Add(hash string, size int64, r io.Reader) error {
	if p.closed {
		return errors.New("packer is closed")
	}
	if size < 0 || size > math.MaxUint32 {
		return fmt.Errorf("object size %d does not fit a pack entry", size)
	}
	if p.f == nil {
		f, err := os.CreateTemp(filepath.Join(p.s.root, packsDir), ".tmp-pack-")
		if err != nil {
			return err
		}
		p.f = f
		p.tmp = f.Name()
		p.size = 0
	}

	start := p.size
	p.zw.Reset(p.f)
	h := blake3.New()
	n, err := io.Copy(io.MultiWriter(p.zw, h), r)
	if err != nil {
		p.rollback(start)
		return err
	}
	if err := p.zw.Close(); err != nil {
		p.rollback(start)
		return err
	}
	end, err := p.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if n != size {
		p.rollback(start)
		return fmt.Errorf("size changed while packing: read %d, expected %d", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != hash {
		p.rollback(start)
		return fmt.Errorf("content changed while packing: hash %s, expected %s", got, hash)
	}
	compLen := end - start
	if compLen <= 0 || compLen > math.MaxUint32 {
		p.rollback(start)
		return fmt.Errorf("compressed size %d does not fit a pack entry", compLen)
	}

	hb, err := decodeHash(hash)
	if err != nil {
		p.rollback(start)
		return err
	}
	p.entries = append(p.entries, packEntry{
		hash:    hb,
		off:     start,
		compLen: uint32(compLen),
		rawLen:  uint32(size),
	})
	p.size = end
	if p.size >= p.opt.TargetSize {
		return p.flush()
	}
	return nil
}

// Close flushes the current pack. It is safe to call more than once.
func (p *Packer) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	err := p.flush()
	p.zw.Close()
	return err
}

func (p *Packer) rollback(off int64) {
	if p.f == nil {
		return
	}
	_ = p.f.Truncate(off)
	if _, err := p.f.Seek(off, io.SeekStart); err != nil {
		// A broken file is dropped by flush when it has no entries; the caller
		// aborts the snapshot anyway.
		p.size = off
		return
	}
	p.size = off
}

func (p *Packer) flush() error {
	if p.f == nil {
		return nil
	}
	f := p.f
	p.f = nil

	if len(p.entries) == 0 {
		_ = f.Close()
		if p.tmp != "" {
			_ = os.Remove(p.tmp)
		}
		p.tmp = ""
		p.size = 0
		return nil
	}
	// The pack must be durable before its index exists: otherwise a crash can
	// leave an index that points at data the filesystem never wrote.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	id, err := randomHex(8)
	if err != nil {
		return err
	}
	base := "pack-" + id
	packPath := filepath.Join(p.s.root, packsDir, base+".pack")
	if err := os.Rename(p.tmp, packPath); err != nil {
		return err
	}
	p.tmp = ""

	idx, err := encodePackIndex(p.entries)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(p.s.root, packsDir, base+".idx"), idx, 0o644); err != nil {
		return fmt.Errorf("write pack index: %w", err)
	}

	p.entries = nil
	p.size = 0
	p.packs++
	p.s.invalidateCatalog()
	return nil
}
