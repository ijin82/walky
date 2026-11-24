// Package scan walks a directory tree and produces a store.Manifest.
package scan

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/zeebo/blake3"

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
}

type candidate struct {
	path string
	rel  string
	info fs.FileInfo
}

// Walk scans root and hashes every regular file. The result is sorted by path
// and ready to be committed.
func Walk(root string, opt Options) (*Result, error) {
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

	var (
		dirs       []store.Entry
		links      []store.Entry
		candidates []candidate
		skipped    []Skipped
	)

	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
			candidates = append(candidates, candidate{path: path, rel: rel, info: info})
		default:
			skipped = append(skipped, Skipped{Path: rel, Reason: "unsupported file type"})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	hashes, err := hashAll(candidates, opt.Workers)
	if err != nil {
		return nil, err
	}

	entries := make([]store.Entry, 0, len(dirs)+len(links)+len(candidates))
	entries = append(entries, dirs...)
	entries = append(entries, links...)
	for i, c := range candidates {
		entries = append(entries, store.Entry{
			Path:    c.rel,
			Type:    store.TypeFile,
			Size:    c.info.Size(),
			Mode:    store.ModeToUnix(c.info.Mode()),
			MtimeNS: c.info.ModTime().UnixNano(),
			Hash:    hashes[i],
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })

	return &Result{Manifest: store.Manifest{Entries: entries}, Skipped: skipped}, nil
}

func excluded(rel string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, rel); ok {
			return true
		}
	}
	return false
}

func hashAll(candidates []candidate, workers int) ([]string, error) {
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
