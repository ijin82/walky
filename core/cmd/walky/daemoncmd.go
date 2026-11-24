package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"walky/internal/api"
	"walky/internal/config"
	"walky/internal/daemon"
	"walky/internal/engine"
	"walky/internal/machineid"
	"walky/internal/state"
	"walky/internal/store"
	"walky/internal/version"
)

func cmdDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	deviceRoot := fs.String("device", "", "use this store directory instead of auto-detection (testing)")
	repoOverride := fs.String("repo", "", "local folder to sync (default: repo.path)")
	interval := fs.Duration("interval", 2*time.Second, "mount table poll interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := setupLog(*verbose)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	repo, err := resolveRepo(cfg, *repoOverride)
	if err != nil {
		return err
	}
	maxSize, err := cfg.Sync.MaxFileSizeBytes()
	if err != nil {
		return err
	}
	idxPath, err := state.IndexPath(repo)
	if err != nil {
		return err
	}
	basePath, err := state.BasePath(repo)
	if err != nil {
		return err
	}
	trashDir, err := state.TrashDir()
	if err != nil {
		return err
	}
	machine, err := machineid.Get()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d := daemon.New(daemon.Config{
		RepoPath:      repo,
		RepoID:        cfg.Repo.RepoID,
		Excludes:      cfg.Exclude.Patterns,
		MaxFileSize:   maxSize,
		Workers:       cfg.Sync.Workers,
		IndexPath:     idxPath,
		Machine:       machine,
		AutoSync:      cfg.Device.AutoSync,
		AutoMount:     cfg.Device.AutoMount,
		StoreOverride: *deviceRoot,
		Interval:      *interval,
		Mode:          string(cfg.Sync.Mode),
		Conflict:      string(cfg.Sync.Conflict),
		BasePath:      basePath,
		TrashDir:      trashDir,
		ConfigPath:    cfg.Path,
		Retention: daemon.RetentionSettings{
			Daily: cfg.Retention.Daily, Weekly: cfg.Retention.Weekly,
			Monthly: cfg.Retention.Monthly, TrashDays: cfg.Retention.TrashDays,
		},
	}, log)

	svc, err := api.New(d, log)
	if err != nil {
		if errors.Is(err, api.ErrAlreadyRunning) {
			log.Info("walky daemon is already running")
			return nil
		}
		return err
	}
	defer func() { _ = svc.Close() }()
	go svc.Run(ctx)

	log.Info("walky daemon started",
		"version", version.Version,
		"folder", repo,
		"mode", cfg.Sync.Mode,
		"auto_sync", cfg.Device.AutoSync,
	)
	return d.Run(ctx)
}

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	wait := fs.Bool("wait", false, "wait for the sync to finish")
	direct := fs.Bool("direct", false, "sync in this process instead of asking the daemon")
	storePath := fs.String("store", "", "store mount point (with --direct)")
	repoPath := fs.String("repo", "", "local folder (with --direct)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLog(*verbose)

	if *direct {
		return directSync(*cfgPath, *storePath, *repoPath)
	}
	if err := serviceSync(*wait); err != nil {
		return err
	}
	if *wait {
		fmt.Println("sync finished")
	} else {
		fmt.Println("sync requested")
	}
	return nil
}

func directSync(cfgPath, storePath, repoPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	repo, err := resolveRepo(cfg, repoPath)
	if err != nil {
		return err
	}
	if storePath == "" {
		return errors.New("--store is required with --direct")
	}
	if err := store.CheckFilesystem(storePath); err != nil {
		return err
	}
	if isInside(repo, storePath) || isInside(storePath, repo) {
		return fmt.Errorf("repo %s and store %s must not contain each other", repo, storePath)
	}
	maxSize, err := cfg.Sync.MaxFileSizeBytes()
	if err != nil {
		return err
	}
	idxPath, err := state.IndexPath(repo)
	if err != nil {
		return err
	}
	basePath, err := state.BasePath(repo)
	if err != nil {
		return err
	}
	trashDir, err := state.TrashDir()
	if err != nil {
		return err
	}
	machine, err := machineid.Get()
	if err != nil {
		return err
	}

	res, err := engine.Sync(context.Background(), engine.Options{
		StoreRoot:   storePath,
		RepoPath:    repo,
		Excludes:    cfg.Exclude.Patterns,
		MaxFileSize: maxSize,
		Workers:     cfg.Sync.Workers,
		IndexPath:   idxPath,
		Machine:     machine,
		Mode:        string(cfg.Sync.Mode),
		Conflict:    string(cfg.Sync.Conflict),
		BasePath:    basePath,
		TrashDir:    trashDir,
	}, nil)
	if err != nil {
		return err
	}
	fmt.Printf("generation %d: %d entries, pushed %d objects (%s), pulled %d, deleted %d, conflicts %d\n",
		res.Gen, res.Entries, res.Objects, humanBytes(res.UploadBytes),
		res.Pulled, res.Deleted, res.Conflicts)
	return nil
}

func cmdEject(args []string) error {
	fs := flag.NewFlagSet("eject", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	if err := serviceEject(); err != nil {
		return err
	}
	fmt.Println("drive ejected")
	return nil
}
