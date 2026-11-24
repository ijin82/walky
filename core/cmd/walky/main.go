// Command walky is the offline drive sync daemon and CLI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
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
  sync                    ask the running daemon to sync now
  eject                   flush, unmount and power off the drive
  init <mountpoint>       initialize a new storage in a mounted directory
  snapshot                commit the local folder as a new generation
  ls                      list a generation (default: latest)
  head                    show the current generation metadata
  restore                 restore a generation into a directory
  verify                  check storage integrity
  attach <mountpoint>     adopt an existing storage                    (M4)
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
	case "sync":
		return cmdSync(args[1:])
	case "eject":
		return cmdEject(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "snapshot":
		return cmdSnapshot(args[1:])
	case "ls":
		return cmdLs(args[1:])
	case "head":
		return cmdHead(args[1:])
	case "restore":
		return cmdRestore(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	case "attach", "gc":
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
	Daemon     bool   `json:"daemon"`
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLog(*verbose)

	// Prefer the running daemon: it knows about the drive and progress.
	if remote, err := serviceStatus(); err == nil {
		return printRemoteStatus(remote, *asJSON)
	}

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
		State:      "no_daemon",
		Daemon:     false,
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Printf("walky %s\n", st.Version)
	fmt.Printf("config:  %s\n", st.ConfigPath)
	fmt.Printf("folder:  %s\n", st.RepoPath)
	fmt.Printf("state:   %s (daemon is not running)\n", st.State)
	return nil
}

func printRemoteStatus(st map[string]any, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Printf("walky %s\n", version.Version)
	fmt.Printf("state:      %v\n", st["state"])
	if v, _ := st["device"].(string); v != "" {
		fmt.Printf("device:     %s\n", v)
	}
	if v, _ := st["store_root"].(string); v != "" {
		fmt.Printf("store:      %s\n", v)
	}
	if v, ok := st["generation"].(int64); ok && v > 0 {
		fmt.Printf("generation: %d\n", v)
	}
	if v, ok := st["last_sync"].(int64); ok && v > 0 {
		fmt.Printf("last sync:  %s\n", time.Unix(v, 0).Local().Format(time.RFC3339))
	}
	if v, ok := st["progress_total"].(uint32); ok && v > 0 {
		done, _ := st["progress_done"].(uint32)
		fmt.Printf("progress:   %d/%d\n", done, v)
	}
	if v, ok := st["free_bytes"].(int64); ok && v > 0 {
		fmt.Printf("free:       %s\n", humanBytes(v))
	}
	if v, _ := st["last_error"].(string); v != "" {
		fmt.Printf("error:      %s\n", v)
	}
	return nil
}
