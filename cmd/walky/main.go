// Command walky is the offline drive sync daemon and CLI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"walky/internal/config"
	"walky/internal/version"
)

const usage = `walky — offline "cloud" on a USB drive

Usage:
  walky <command> [flags]

Commands:
  version                 print version
  status                  print daemon/storage state
  daemon                  run the sync daemon in the foreground
  init <device>           initialize a new storage on a block device   (M1)
  attach <mountpoint>     adopt an existing storage                    (M1)
  sync                    request a sync from the running daemon       (M3)
  restore <mountpoint>    restore the latest snapshot                  (M1)
  verify                  check storage integrity                      (M1)
  gc                      garbage-collect unreferenced objects          (M5)

Global flags:
  -config <path>   config file (default: $XDG_CONFIG_HOME/walky/config.toml)
  -v               verbose logging (debug level)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "walky: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "version", "-version", "--version":
		fmt.Println(version.String())
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "status":
		return cmdStatus(args[1:])
	case "daemon":
		return cmdDaemon(args[1:])
	case "init", "attach", "sync", "restore", "verify", "gc":
		return fmt.Errorf("%q is not implemented yet (see docs/DESIGN.md milestones)", args[0])
	default:
		return fmt.Errorf("unknown command %q (try: walky help)", args[0])
	}
}

// commonFlags registers flags shared by all subcommands.
func commonFlags(fs *flag.FlagSet) (cfgPath *string, verbose *bool) {
	cfgPath = fs.String("config", "", "config file path")
	verbose = fs.Bool("v", false, "verbose logging")
	return
}

func setupLog(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

type status struct {
	Version    string `json:"version"`
	ConfigPath string `json:"config_path"`
	RepoPath   string `json:"repo_path"`
	State      string `json:"state"`
	Device     any    `json:"device"`
	LastSync   *int64 `json:"last_sync,omitempty"`
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLog(*verbose)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	repoPath, err := cfg.ExpandedRepoPath()
	if err != nil {
		return err
	}

	st := status{
		Version:    version.Version,
		ConfigPath: cfg.Path,
		RepoPath:   repoPath,
		State:      "idle", // M3: state comes from the daemon over D-Bus
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Printf("walky %s\n", st.Version)
	fmt.Printf("config:  %s\n", st.ConfigPath)
	fmt.Printf("folder:  %s\n", st.RepoPath)
	fmt.Printf("state:   %s\n", st.State)
	fmt.Printf("device:  not tracked yet (M3)\n")
	return nil
}

func cmdDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := setupLog(*verbose)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	repoPath, err := cfg.ExpandedRepoPath()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("walky daemon starting",
		"version", version.Version,
		"config", cfg.Path,
		"folder", repoPath,
		"mode", cfg.Sync.Mode,
	)
	log.Warn("skeleton build: drive detection and sync are not implemented yet")

	// M0 placeholder: stay alive so systemd/session behaviour can be observed.
	// M3 replaces this with the udisks2 monitor + D-Bus service.
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("walky daemon stopped")
			return nil
		case <-tick.C:
			log.Debug("alive")
		}
	}
}
