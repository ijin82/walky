// Package scan walks a directory tree and produces a store.Manifest.
package scan

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/zeebo/blake3"

	"walky/internal/index"
	"walky/internal/store"
)

// Options tune a scan.
type Options struct {
	// Excludes holds gitignore-style glob patterns, matched against
	// slash-separated paths relative to the scan root.
	Excludes []string
	// MaxFileSize skips regular files larger than this (0 means no limit).
	MaxFileSize int64
	// Workers is the number of hashing goroutines (0 means auto).
	Workers int
	// Index caches hashes between scans. Optional.
	Index *index.DB
	// ForceRescan ignores cached hashes and rehashes every file.
	ForceRescan bool
	// Context cancels the walk and the hashing workers. Optional.
	Context context.Context
}

// Skipped records a path that was not included in the manifest.
type Skipped struct {
	Path   string
	Reason string
}

// Result is the outcome of a scan.
type Result struct {
	Manifest store.Manifest
	Skipped  []Skipped
	// Cached is the number of files whose hash came from the index.
	Cached int
	// Hashed is the number of files that had to be read.
	Hashed int
}

type candidate struct {
	path    string
	rel     string
	info    fs.FileInfo
	dev     uint64
	ino     uint64
	mtimeNS int64
	ctimeNS int64
}

// Walk scans root and hashes new or changed regular files. The result is
// sorted by path and ready to be committed.
func Walk(root string, opt Options) (*Result, error) {
	ctx := opt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("scan root: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("scan root %s is not a directory", abs)
	}

	var cache map[string]index.Record
	if opt.Index != nil && !opt.ForceRescan {
		cache, err = opt.Index.LoadAll()
		if err != nil {
			return nil, fmt.Errorf("load index: %w", err)
		}
	}

	var (
		dirs       []store.Entry
		links      []store.Entry
		files      []store.Entry
		candidates []candidate
		skipped    []Skipped
		updates    []index.Record
		cached     int
	)
	// seen holds every regular file that still exists, for index pruning.
	seen := make(map[string]struct{})

	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == abs {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excluded(rel, opt.Excludes) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mtime := info.ModTime().UnixNano()
		mode := store.ModeToUnix(info.Mode())

		switch {
		case d.IsDir():
			dirs = append(dirs, store.Entry{Path: rel, Type: store.TypeDir, Mode: mode, MtimeNS: mtime})
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			links = append(links, store.Entry{
				Path: rel, Type: store.TypeSymlink, Mode: mode, MtimeNS: mtime, Target: target,
			})
		case info.Mode().IsRegular():
			if opt.MaxFileSize > 0 && info.Size() > opt.MaxFileSize {
				skipped = append(skipped, Skipped{Path: rel, Reason: "larger than max_file_size"})
				return nil
			}
			dev, ino, mtimeNS, ctimeNS := statFields(info)
			seen[rel] = struct{}{}

			if rec, ok := cache[rel]; ok &&
				rec.Dev == dev && rec.Ino == ino && rec.Size == info.Size() &&
				rec.MtimeNS == mtimeNS && rec.CtimeNS == ctimeNS && looksLikeHash(rec.Hash) {
				files = append(files, store.Entry{
					Path: rel, Type: store.TypeFile, Size: info.Size(),
					Mode: mode, MtimeNS: mtime, Hash: rec.Hash,
				})
				cached++
				if rec.Mode != mode {
					rec.Mode = mode
					updates = append(updates, rec)
				}
				return nil
			}
			candidates = append(candidates, candidate{
				path: path, rel: rel, info: info,
				dev: dev, ino: ino, mtimeNS: mtimeNS, ctimeNS: ctimeNS,
			})
		default:
			skipped = append(skipped, Skipped{Path: rel, Reason: "unsupported file type"})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	hashes, err := hashAll(ctx, candidates, opt.Workers)
	if err != nil {
		return nil, err
	}
	for i, c := range candidates {
		hash := hashes[i]
		updates = append(updates, index.Record{
			Path: c.rel, Dev: c.dev, Ino: c.ino, Size: c.info.Size(),
			MtimeNS: c.mtimeNS, CtimeNS: c.ctimeNS,
			Mode: store.ModeToUnix(c.info.Mode()), Hash: hash,
		})
		files = append(files, store.Entry{
			Path: c.rel, Type: store.TypeFile, Size: c.info.Size(),
			Mode: store.ModeToUnix(c.info.Mode()), MtimeNS: c.info.ModTime().UnixNano(),
			Hash: hash,
		})
	}

	entries := make([]store.Entry, 0, len(dirs)+len(links)+len(files))
	entries = append(entries, dirs...)
	entries = append(entries, links...)
	entries = append(entries, files...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })

	if opt.Index != nil {
		if err := opt.Index.Apply(updates, seen); err != nil {
			return nil, fmt.Errorf("update index: %w", err)
		}
	}

	return &Result{
		Manifest: store.Manifest{Entries: entries},
		Skipped:  skipped,
		Cached:   cached,
		Hashed:   len(candidates),
	}, nil
}

func statFields(info fs.FileInfo) (dev, ino uint64, mtimeNS, ctimeNS int64) {
	mtimeNS = info.ModTime().UnixNano()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		dev = uint64(st.Dev)
		ino = st.Ino
		ctimeNS = st.Ctim.Sec*1e9 + st.Ctim.Nsec
	}
	return dev, ino, mtimeNS, ctimeNS
}

func looksLikeHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func excluded(rel string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, rel); ok {
			return true
		}
	}
	return false
}

func hashAll(ctx context.Context, candidates []candidate, workers int) ([]string, error) {
	results := make([]string, len(candidates))
	if len(candidates) == 0 {
		return results, nil
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
		if workers > 8 {
			workers = 8
		}
	}
	if workers > len(candidates) {
		workers = len(candidates)
	}

	var (
		next     atomic.Int64
		firstErr atomic.Value
		wg       sync.WaitGroup
	)
	next.Store(-1)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1))
				if i >= len(candidates) || firstErr.Load() != nil {
					return
				}
				if err := ctx.Err(); err != nil {
					firstErr.CompareAndSwap(nil, err)
					return
				}
				hash, err := hashFile(candidates[i].path)
				if err != nil {
					firstErr.CompareAndSwap(nil, fmt.Errorf("hash %s: %w", candidates[i].rel, err))
					return
				}
				results[i] = hash
			}
		}()
	}
	wg.Wait()
	if err, ok := firstErr.Load().(error); ok && err != nil {
		return nil, err
	}
	return results, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := blake3.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
