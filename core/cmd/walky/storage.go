package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"walky/internal/config"
	"walky/internal/machineid"
	"walky/internal/scan"
	"walky/internal/store"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	force := fs.Bool("force", false, "adopt a non-empty directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)
	if fs.NArg() != 1 {
		return errors.New("usage: walky init <mountpoint> [--force]")
	}

	st, err := store.Init(fs.Arg(0), *force)
	if err != nil {
		return err
	}
	m := st.Marker()
	fmt.Printf("initialized store at %s\n", st.Root())
	fmt.Printf("repo_id: %s\n", m.RepoID)
	fmt.Printf("format:  %d\n", m.Format)
	fmt.Printf("\nPin the drive identity in ~/.config/walky/config.toml:\n\n[repo]\npath = \"~/Sync\"\nrepo_id = %q\n", m.RepoID)
	return nil
}

func cmdSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	repoPath := fs.String("repo", "", "local folder to snapshot (default: repo.path)")
	message := fs.String("message", "", "optional commit message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLog(*verbose)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	repo, err := resolveRepo(cfg, *repoPath)
	if err != nil {
		return err
	}
	if isInside(repo, st.Root()) || isInside(st.Root(), repo) {
		return fmt.Errorf("repo %s and store %s must not contain each other", repo, st.Root())
	}
	maxSize, err := cfg.Sync.MaxFileSizeBytes()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "scanning %s\n", repo)
	res, err := scan.Walk(repo, scan.Options{
		Excludes:    cfg.Exclude.Patterns,
		MaxFileSize: maxSize,
		Workers:     cfg.Sync.Workers,
	})
	if err != nil {
		return err
	}
	entries, files, bytesTotal := res.Manifest.Stats()
	fmt.Fprintf(os.Stderr, "scanned: %d entries, %d files, %s\n", entries, files, humanBytes(bytesTotal))
	for _, s := range res.Skipped {
		fmt.Fprintf(os.Stderr, "skip: %s (%s)\n", s.Path, s.Reason)
	}

	uploaded, uploadedBytes, err := putMissingObjects(st, repo, &res.Manifest)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "stored: %d new objects, %s\n", uploaded, humanBytes(uploadedBytes))

	id, err := machineid.Get()
	if err != nil {
		return err
	}
	commit, err := st.Commit(&res.Manifest, store.CommitOptions{Machine: id, Message: *message})
	if err != nil {
		return err
	}
	if !commit.Changed {
		fmt.Printf("generation %d: no changes\n", commit.Gen)
		return nil
	}
	fmt.Printf("generation %d: committed %d entries, %s\n", commit.Gen, entries, humanBytes(bytesTotal))
	return nil
}

func putMissingObjects(st *store.Store, repo string, m *store.Manifest) (int, int64, error) {
	var count int
	var total int64
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.Type != store.TypeFile {
			continue
		}
		has, err := st.HasObject(e.Hash)
		if err != nil {
			return count, total, err
		}
		if has {
			continue
		}
		path := filepath.Join(repo, filepath.FromSlash(e.Path))
		f, err := os.Open(path)
		if err != nil {
			return count, total, fmt.Errorf("open %s: %w", e.Path, err)
		}
		hash, size, err := st.PutObject(f)
		_ = f.Close()
		if err != nil {
			return count, total, fmt.Errorf("store %s: %w", e.Path, err)
		}
		if hash != e.Hash {
			return count, total, fmt.Errorf("%s changed during scan (hash %s, expected %s)", e.Path, hash, e.Hash)
		}
		count++
		total += size
	}
	return count, total, nil
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	gen := fs.Int64("gen", 0, "generation to list (default: latest)")
	long := fs.Bool("long", false, "show hashes and modes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	m, meta, err := loadSnapshot(st, *gen)
	if err != nil {
		return err
	}
	fmt.Printf("generation %d (%s, %d entries)\n", meta.Gen, meta.Created.Local().Format(time.RFC3339), len(m.Entries))
	for _, e := range m.Entries {
		switch e.Type {
		case store.TypeDir:
			fmt.Printf("d %s/\n", e.Path)
		case store.TypeSymlink:
			fmt.Printf("l %s -> %s\n", e.Path, e.Target)
		case store.TypeFile:
			if *long {
				fmt.Printf("f %10s %04o %s  %s\n", humanBytes(e.Size), e.Mode, e.Hash[:12], e.Path)
			} else {
				fmt.Printf("f %10s %s\n", humanBytes(e.Size), e.Path)
			}
		}
	}
	return nil
}

func cmdHead(args []string) error {
	fs := flag.NewFlagSet("head", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	head, err := st.Head()
	if err != nil {
		return err
	}
	metaData, err := os.ReadFile(filepath.Join(st.Root(), "snapshots",
		fmt.Sprintf("%08d.meta.json", head.Gen)))
	if err != nil {
		return err
	}
	var meta store.Meta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(meta)
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	dest := fs.String("dest", "", "destination directory (must be empty)")
	gen := fs.Int64("gen", 0, "generation to restore (default: latest)")
	force := fs.Bool("force", false, "overwrite an existing destination")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	if *dest == "" {
		return errors.New("--dest is required")
	}
	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	m, meta, err := loadSnapshot(st, *gen)
	if err != nil {
		return err
	}
	if err := st.Restore(*dest, m, *force); err != nil {
		return err
	}
	fmt.Printf("restored generation %d to %s\n", meta.Gen, *dest)
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	full := fs.Bool("full", false, "re-hash every object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	rep, err := st.Verify(*full)
	if err != nil {
		return err
	}
	fmt.Printf("generation %d: %d entries, %d objects, %s\n",
		rep.Gen, rep.Entries, rep.Objects, humanBytes(rep.Bytes))
	if rep.OK() {
		fmt.Println("ok")
		return nil
	}
	for _, p := range rep.Missing {
		fmt.Printf("missing object: %s\n", p)
	}
	for _, p := range rep.SizeMismatch {
		fmt.Printf("size mismatch: %s\n", p)
	}
	for _, p := range rep.Corrupt {
		fmt.Printf("corrupt object: %s\n", p)
	}
	return fmt.Errorf("integrity check failed")
}

func openStore(path string) (*store.Store, error) {
	if path == "" {
		return nil, errors.New("--store is required (mount point of the Walky drive)")
	}
	return store.Open(path)
}

func resolveRepo(cfg config.Config, override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return cfg.ExpandedRepoPath()
}

func loadSnapshot(st *store.Store, gen int64) (*store.Manifest, *store.Meta, error) {
	if gen > 0 {
		return st.ReadSnapshot(gen)
	}
	return st.Latest()
}

func isInside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
