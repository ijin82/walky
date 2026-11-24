package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"walky/internal/config"
	"walky/internal/state"
	"walky/internal/store"
)

func cmdTrash(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: walky trash <list|empty> [flags]")
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Println("usage: walky trash <list|empty> [flags]")
		fmt.Println("  list   show local trash and, with --store, the drive trash")
		fmt.Println("  empty  clear local trash; --store clears the drive trash and runs GC")
		return nil
	}
	switch args[0] {
	case "list":
		return cmdTrashList(args[1:])
	case "empty":
		return cmdTrashEmpty(args[1:])
	default:
		return fmt.Errorf("unknown trash command %q", args[0])
	}
}

func cmdTrashList(args []string) error {
	fs := flag.NewFlagSet("trash list", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "also show the drive trash")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgPath
	setupLog(*verbose)

	local, err := state.LocalTrashStats()
	if err != nil {
		return err
	}
	fmt.Printf("local trash: %d files, %s\n", local.Files, humanBytes(local.Bytes))
	if *storePath != "" {
		st, err := openStore(*storePath)
		if err != nil {
			return err
		}
		records, err := st.TrashRecords()
		if err != nil {
			return err
		}
		var entries int
		for _, rec := range records {
			entries += len(rec.Entries)
			fmt.Printf("generation %d: %d entries\n", rec.Gen, len(rec.Entries))
			for _, e := range rec.Entries {
				fmt.Printf("  %s (%s)\n", e.Path, humanBytes(e.Size))
			}
		}
		fmt.Printf("drive trash: %d records, %d entries\n", len(records), entries)
	}
	return nil
}

func cmdTrashEmpty(args []string) error {
	fs := flag.NewFlagSet("trash empty", flag.ContinueOnError)
	cfgPath, verbose := commonFlags(fs)
	storePath := fs.String("store", "", "empty the drive trash and reclaim space")
	clearLocal := fs.Bool("local", false, "also clear the machine's local trash")
	dryRun := fs.Bool("dry-run", false, "report what would be removed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLog(*verbose)

	// Without --store the command only touches the local trash; that is the
	// safe default for a plain "trash empty".
	doLocal := *clearLocal || *storePath == ""
	if doLocal {
		st, err := state.LocalTrashStats()
		if err != nil {
			return err
		}
		fmt.Printf("local trash: %d files, %s\n", st.Files, humanBytes(st.Bytes))
		if !*dryRun {
			if _, err := state.ClearLocalTrash(); err != nil {
				return err
			}
			fmt.Println("local trash cleared")
		}
	}

	if *storePath == "" {
		if *dryRun {
			fmt.Println("dry run: nothing was deleted")
		}
		return nil
	}

	st, err := openStore(*storePath)
	if err != nil {
		return err
	}
	records, err := st.TrashRecords()
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("drive trash: %d records would be removed\n", len(records))
		fmt.Println("dry run: nothing was deleted")
		return nil
	}
	removed, bytes, err := st.ClearTrash()
	if err != nil {
		return err
	}
	fmt.Printf("drive trash cleared: %d records, %s\n", removed, humanBytes(bytes))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	rep, err := st.GC(store.GCOptions{
		KeepDaily:   cfg.Retention.Daily,
		KeepWeekly:  cfg.Retention.Weekly,
		KeepMonthly: cfg.Retention.Monthly,
		TrashDays:   0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: garbage collection skipped: %v\n", err)
		return nil
	}
	fmt.Printf("reclaimed: %s (%d objects, %d packs)\n",
		humanBytes(rep.FreedBytes), rep.RemovedObjects, rep.RemovedPacks)
	return nil
}
