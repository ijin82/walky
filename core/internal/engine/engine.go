// Package engine runs synchronization: scan the repository, merge with the
// drive, upload and download objects, and commit a generation. It is shared by
// the CLI and the daemon.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"walky/internal/index"
	"walky/internal/scan"
	"walky/internal/store"
)

// Modes understood by Sync.
const (
	ModePushOnly = "push-only"
	ModeTwoWay   = "two-way"
)

// ErrDifferentStore means the local base belongs to another repository.
var ErrDifferentStore = errors.New("base belongs to a different store")

// Conflict policies.
const (
	ConflictCopy      = "copy"
	ConflictNewest    = "newest"
	ConflictLocalWins = "local-wins"
)

// Options describes one synchronization run.
type Options struct {
	StoreRoot   string
	RepoPath    string
	Excludes    []string
	MaxFileSize int64
	Workers     int
	IndexPath   string
	Machine     string
	Hostname    string
	Message     string
	ForceRescan bool

	// Mode is ModePushOnly (default) or ModeTwoWay.
	Mode string
	// Conflict is one of the Conflict* policies.
	Conflict string
	// BasePath is the local base manifest used by two-way mode.
	BasePath string
	// TrashDir receives files deleted locally by a sync.
	TrashDir string
	// StoreRepoID is the repo_id of the mounted store; when it differs from
	// the base metadata the run needs Recover.
	StoreRepoID string
	// Recover performs an additive recovery merge: no deletions, conflicts keep
	// both variants. It is used after a store is rebuilt or adopted.
	Recover bool
}

// PreviewResult summarizes a plan without applying it.
type PreviewResult struct {
	Push      int
	Pull      int
	Conflicts int
	Deletes   int
	Total     int
}

// Progress is one step of a run.
type Progress struct {
	Phase string // "scan", "plan", "pull", "push", "commit"
	Done  int
	Total int
	Bytes int64
	Size  int64
	Path  string
	Op    string // "push", "pull", "delete" or "conflict"
}

// Result summarizes a completed run.
type Result struct {
	Gen         int64
	Changed     bool
	Entries     int
	Files       int
	Bytes       int64
	Cached      int
	Hashed      int
	Objects     int
	Packs       int
	UploadBytes int64
	Pulled      int
	Deleted     int
	Conflicts   int
	Skipped     []scan.Skipped
}

// Run performs a push-only snapshot, kept for the snapshot command.
func Run(ctx context.Context, opt Options, progress func(Progress)) (Result, error) {
	opt.Mode = ModePushOnly
	return Sync(ctx, opt, progress)
}

// Sync performs one synchronization according to opt.Mode.
func Sync(ctx context.Context, opt Options, progress func(Progress)) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.Mode == "" {
		opt.Mode = ModePushOnly
	}
	switch opt.Mode {
	case ModePushOnly:
		return pushOnly(ctx, opt, progress)
	case ModeTwoWay:
		return twoWay(ctx, opt, progress)
	default:
		return Result{}, fmt.Errorf("unknown sync mode %q", opt.Mode)
	}
}

func pushOnly(ctx context.Context, opt Options, progress func(Progress)) (Result, error) {
	st, err := store.Open(opt.StoreRoot)
	if err != nil {
		return Result{}, err
	}
	res, err := scanRepo(ctx, opt, progress)
	if err != nil {
		return Result{}, err
	}
	m := &res.Manifest
	durable, err := st.CommittedHashes()
	if err != nil {
		return Result{}, err
	}
	objects, packs, uploadBytes, err := upload(ctx, st, opt.RepoPath, m, durable, progress)
	if err != nil {
		return Result{}, err
	}
	report(progress, Progress{Phase: "commit"})
	commit, err := st.Commit(m, store.CommitOptions{Machine: opt.Machine, Message: opt.Message})
	if err != nil {
		return Result{}, err
	}
	entries, files, bytesTotal := m.Stats()
	return Result{
		Gen: commit.Gen, Changed: commit.Changed,
		Entries: entries, Files: files, Bytes: bytesTotal,
		Cached: res.Cached, Hashed: res.Hashed,
		Objects: objects, Packs: packs, UploadBytes: uploadBytes,
		Skipped: res.Skipped,
	}, nil
}

func scanRepo(ctx context.Context, opt Options, progress func(Progress)) (*scan.Result, error) {
	var idx *index.DB
	if opt.IndexPath != "" {
		db, err := index.Open(opt.IndexPath)
		if err != nil {
			return nil, fmt.Errorf("open index: %w", err)
		}
		defer db.Close()
		idx = db
	}
	report(progress, Progress{Phase: "scan"})
	return scan.Walk(opt.RepoPath, scan.Options{
		Context:     ctx,
		Excludes:    opt.Excludes,
		MaxFileSize: opt.MaxFileSize,
		Workers:     opt.Workers,
		Index:       idx,
		ForceRescan: opt.ForceRescan,
	})
}

// upload writes every file object that is not yet durable, packing small files.
func upload(ctx context.Context, st *store.Store, repo string, m *store.Manifest, durable map[string]struct{}, progress func(Progress)) (objects, packs int, bytes int64, err error) {
	packer := st.NewPacker(store.DefaultPackOptions())
	defer func() { _ = packer.Close() }()

	seen := make(map[string]struct{}, len(m.Entries))
	done := 0
	for i := range m.Entries {
		if err := ctx.Err(); err != nil {
			return 0, 0, 0, err
		}
		e := &m.Entries[i]
		done++
		if e.Type != store.TypeFile {
			continue
		}
		if _, ok := seen[e.Hash]; ok {
			continue
		}
		seen[e.Hash] = struct{}{}
		if _, ok := durable[e.Hash]; ok {
			continue
		}
		path := filepath.Join(repo, filepath.FromSlash(e.Path))
		f, err := os.Open(path)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("open %s: %w", e.Path, err)
		}
		if e.Size >= packer.Threshold() {
			hash, size, err := st.PutObject(f)
			_ = f.Close()
			if err != nil {
				return 0, 0, 0, fmt.Errorf("store %s: %w", e.Path, err)
			}
			if hash != e.Hash {
				return 0, 0, 0, fmt.Errorf("%s changed during scan (hash %s, expected %s)", e.Path, hash, e.Hash)
			}
			bytes += size
		} else {
			err := packer.Add(e.Hash, e.Size, f)
			_ = f.Close()
			if err != nil {
				return 0, 0, 0, fmt.Errorf("pack %s: %w", e.Path, err)
			}
			bytes += e.Size
		}
		objects++
		report(progress, Progress{
			Phase: "push", Done: done, Total: len(m.Entries),
			Bytes: bytes, Size: e.Size, Path: e.Path, Op: "push",
		})
	}
	if err := packer.Close(); err != nil {
		return 0, 0, 0, err
	}
	return objects, packer.Packs(), bytes, nil
}

func report(progress func(Progress), p Progress) {
	if progress != nil {
		progress(p)
	}
}
