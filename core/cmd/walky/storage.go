package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"walky/internal/config"
	"walky/internal/device"
	"walky/internal/engine"
	"walky/internal/machineid"
	"walky/internal/setup"
	"walky/internal/state"
	"walky/internal/store"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	force := fs.Bool("force", false, "adopt a non-empty directory")
	repo := fs.String("repo", "", "also configure this folder as the sync root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)
	if fs.NArg() != 1 {
		return errors.New("usage: walky init <mountpoint> [--force]")
	}

	res, err := setup.Initialize(fs.Arg(0), *force, filesystemCheck)
	if err != nil {
		return err
	}
	fmt.Printf("initialized store at %s\n", res.StoreRoot)
	fmt.Printf("repo_id: %s\n", res.RepoID)
	fmt.Printf("format:  %d\n", res.Format)
	if *repo != "" {
		cfgPath := *cfgPath
		if cfgPath == "" {
			cfgPath, err = config.DefaultPath()
			if err != nil {
				return err
			}
		}
		if err := setup.ConfigureRepo(cfgPath, *repo, res.RepoID); err != nil {
			return err
		}
		fmt.Printf("\nconfigured %s with repo_id %s\n", *repo, res.RepoID)
		return nil
	}
	fmt.Printf("\nPin the drive identity in ~/.config/walky/config.toml:\n\n[repo]\npath = \"~/Sync\"\nrepo_id = %q\n", res.RepoID)
	fmt.Println("\nTip: if the desktop does not automount the drive, give the filesystem a label")
	fmt.Println("     (e2label /dev/sdX WALKY) and match it with [device] label in the config.")
	if encrypted, known := device.EncryptionStatus(res.StoreRoot); known && !encrypted {
		fmt.Fprintln(os.Stderr, "warning: this drive is not encrypted. LUKS2 is strongly recommended: a lost or stolen drive exposes all data.")
	}
	return nil
}

func cmdSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	repoPath := fs.String("repo", "", "local folder to snapshot (default: repo.path)")
	message := fs.String("message", "", "optional commit message")
	forceRescan := fs.Bool("force-rescan", false, "ignore the hash index and rehash every file")
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

	id, err := machineid.Get()
	if err != nil {
		return err
	}
	idxPath, err := state.IndexPath(repo)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "scanning %s\n", repo)
	res, err := engine.Run(context.Background(), engine.Options{
		StoreRoot:   st.Root(),
		RepoPath:    repo,
		Excludes:    cfg.Exclude.Patterns,
		MaxFileSize: maxSize,
		Workers:     cfg.Sync.Workers,
		IndexPath:   idxPath,
		Machine:     id,
		Message:     *message,
		ForceRescan: *forceRescan,
	}, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "scanned: %d entries, %d files, %s (%d cached, %d hashed)\n",
		res.Entries, res.Files, humanBytes(res.Bytes), res.Cached, res.Hashed)
	for _, sk := range res.Skipped {
		fmt.Fprintf(os.Stderr, "skip: %s (%s)\n", sk.Path, sk.Reason)
	}
	fmt.Fprintf(os.Stderr, "stored: %d new objects in %d packs, %s\n",
		res.Objects, res.Packs, humanBytes(res.UploadBytes))

	if !res.Changed {
		fmt.Printf("generation %d: no changes\n", res.Gen)
		return nil
	}
	fmt.Printf("generation %d: committed %d entries, %s\n", res.Gen, res.Entries, humanBytes(res.Bytes))
	return nil
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
	if err := filesystemCheck(path); err != nil {
		return nil, err
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

func cmdGC(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "mount point of the Walky drive")
	dryRun := fs.Bool("dry-run", false, "report what would be removed")
	daily := fs.Int("daily", -1, "daily generations to keep (default: config)")
	weekly := fs.Int("weekly", -1, "weekly generations to keep (default: config)")
	monthly := fs.Int("monthly", -1, "monthly generations to keep (default: config)")
	trashDays := fs.Int("trash-days", -1, "days to keep deleted entries (default: config)")
	machineDays := fs.Int("machine-days", -1, "days to keep idle machine records (default: config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	machine, _ := machineid.Get()
	opts := store.GCOptions{
		KeepDaily:         cfg.Retention.Daily,
		KeepWeekly:        cfg.Retention.Weekly,
		KeepMonthly:       cfg.Retention.Monthly,
		TrashDays:         cfg.Retention.TrashDays,
		MachineMaxAgeDays: cfg.Retention.MachineDays,
		KeepMachine:       machine,
		DryRun:            *dryRun,
	}
	if *daily >= 0 {
		opts.KeepDaily = *daily
	}
	if *weekly >= 0 {
		opts.KeepWeekly = *weekly
	}
	if *monthly >= 0 {
		opts.KeepMonthly = *monthly
	}
	if *trashDays >= 0 {
		opts.TrashDays = *trashDays
	}
	if *machineDays >= 0 {
		opts.MachineMaxAgeDays = *machineDays
	}

	rep, err := st.GC(opts)
	if err != nil {
		return err
	}
	if rep.DryRun {
		fmt.Println("dry run: nothing was deleted")
	}
	fmt.Printf("kept generations:    %v\n", rep.KeptGenerations)
	if len(rep.RemovedGenerations) > 0 {
		fmt.Printf("removed generations: %v\n", rep.RemovedGenerations)
	}
	fmt.Printf("removed objects:     %d\n", rep.RemovedObjects)
	fmt.Printf("removed packs:       %d (compacted %d)\n", rep.RemovedPacks, rep.CompactedPacks)
	fmt.Printf("expired trash files: %d\n", rep.ExpiredTrash)
	fmt.Printf("removed machines:    %d\n", rep.RemovedMachines)
	fmt.Printf("freed:               %s\n", humanBytes(rep.FreedBytes))
	for _, w := range rep.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	return nil
}
