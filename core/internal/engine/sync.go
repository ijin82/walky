package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeebo/blake3"
	"golang.org/x/sys/unix"

	"walky/internal/store"
)

type planKind int

const (
	actNone planKind = iota
	actPush
	actPull
	actDeleteLocal
	actDeleteRemote
	actConflictRemote
	actConflictLocal
)

type planItem struct {
	Path   string
	Kind   planKind
	Local  store.Entry
	Remote store.Entry
}

type optEntry struct {
	e  store.Entry
	ok bool
}

func (o optEntry) isNil() bool { return !o.ok }

func entryMap(m *store.Manifest) map[string]store.Entry {
	out := make(map[string]store.Entry, len(m.Entries))
	for _, e := range m.Entries {
		out[e.Path] = e
	}
	return out
}

func sameEntry(a, b optEntry) bool {
	if a.ok != b.ok {
		return false
	}
	if !a.ok {
		return true
	}
	return a.e.Type == b.e.Type && a.e.Hash == b.e.Hash &&
		a.e.Target == b.e.Target && a.e.Mode == b.e.Mode
}

func twoWay(ctx context.Context, opt Options, progress func(Progress)) (Result, error) {
	st, err := store.Open(opt.StoreRoot)
	if err != nil {
		return Result{}, err
	}
	res, err := scanRepo(ctx, opt, progress)
	if err != nil {
		return Result{}, err
	}
	local := &res.Manifest

	remote := &store.Manifest{}
	var parent int64
	latest, meta, err := st.Latest()
	switch {
	case errors.Is(err, store.ErrNoHead):
	case err != nil:
		return Result{}, err
	default:
		remote = latest
		parent = meta.Gen
	}

	if opt.StoreRepoID == "" {
		opt.StoreRepoID = st.Marker().RepoID
	}
	base := loadBase(opt.BasePath)
	plan, err := planFor(local, remote, base, opt)
	if err != nil {
		return Result{}, err
	}
	report(progress, Progress{Phase: "plan", Total: len(plan)})

	pullBytes, pushBytes := planSizes(plan)
	if err := checkSpace(opt.StoreRoot, pushBytes); err != nil {
		return Result{}, err
	}
	if err := checkSpace(opt.RepoPath, pullBytes); err != nil {
		return Result{}, err
	}

	host := opt.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	host = sanitizeHost(host)
	now := time.Now()

	target := entryMap(remote)
	durable, err := st.CommittedHashes()
	if err != nil {
		return Result{}, err
	}

	var pulled, deleted, conflicts int
	trashedLocal := make(map[string]bool)

	// Pull first: downloads are applied even if the later push fails.
	done := 0
	for _, item := range plan {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		switch item.Kind {
		case actPull:
			if err := applyRemote(st, opt.RepoPath, item.Remote); err != nil {
				return Result{}, err
			}
			pulled++
			done++
			report(progress, Progress{Phase: "pull", Done: done, Total: len(plan), Path: item.Path, Op: "pull"})
		case actDeleteLocal:
			if err := moveToTrash(opt.RepoPath, opt.TrashDir, item.Path, now); err != nil {
				return Result{}, err
			}
			trashedLocal[item.Path] = true
			deleted++
			done++
			report(progress, Progress{Phase: "pull", Done: done, Total: len(plan), Path: item.Path, Op: "delete"})
		case actConflictRemote:
			conflictRel, localEntry, err := resolveConflictCopy(st, opt.RepoPath, item, host, now)
			if err != nil {
				return Result{}, err
			}
			target[conflictRel] = localEntry
			conflicts++
			done++
			report(progress, Progress{Phase: "pull", Done: done, Total: len(plan), Path: item.Path, Op: "conflict"})
		}
	}

	// Apply remaining plan items to the target manifest.
	for _, item := range plan {
		switch item.Kind {
		case actPush, actConflictLocal:
			target[item.Path] = item.Local
			if item.Kind == actConflictLocal {
				conflicts++
				report(progress, Progress{Phase: "pull", Path: item.Path, Op: "conflict"})
			}
		case actDeleteRemote:
			delete(target, item.Path)
			deleted++
			report(progress, Progress{Phase: "pull", Path: item.Path, Op: "delete"})
		case actDeleteLocal:
			delete(target, item.Path)
		}
	}

	// Remove empty directories that were deleted locally.
	pruneEmptyDirs(opt.RepoPath, trashedLocal)

	ensureParents(target)
	result := &store.Manifest{Entries: make([]store.Entry, 0, len(target))}
	for _, e := range target {
		result.Entries = append(result.Entries, e)
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path })

	objects, packs, uploadBytes, err := upload(ctx, st, opt.RepoPath, result, durable, progress)
	if err != nil {
		return Result{}, err
	}

	// Directory metadata is applied after content so timestamps survive.
	if err := applyDirMetadata(opt.RepoPath, remote); err != nil {
		return Result{}, err
	}

	report(progress, Progress{Phase: "commit"})
	commit, err := st.Commit(result, store.CommitOptions{
		Machine:      opt.Machine,
		Message:      opt.Message,
		ExpectParent: &parent,
	})
	if err != nil {
		return Result{}, err
	}
	if commit.Changed {
		if err := st.SaveTrash(commit.Gen, removedEntries(remote, target)); err != nil {
			return Result{}, err
		}
	}
	// A polling sync with no changes must not rewrite local state or the drive.
	needBase := commit.Changed
	if opt.BasePath != "" {
		if _, err := os.Stat(opt.BasePath); err != nil {
			needBase = true
		}
		if loadBaseRepoID(opt.BasePath) != opt.StoreRepoID {
			needBase = true
		}
	}
	if needBase {
		if err := saveBase(opt.BasePath, commit.Gen, result, opt.Machine, opt.StoreRepoID); err != nil {
			return Result{}, err
		}
	}
	if opt.Machine != "" && (commit.Changed || needBase) {
		if err := st.SaveMachine(store.Machine{
			ID: opt.Machine, Hostname: host, BaseGen: commit.Gen, LastSync: time.Now().UTC(),
		}); err != nil {
			return Result{}, err
		}
	}

	entries, files, bytesTotal := result.Stats()
	return Result{
		Gen: commit.Gen, Changed: commit.Changed,
		Entries: entries, Files: files, Bytes: bytesTotal,
		Cached: res.Cached, Hashed: res.Hashed,
		Objects: objects, Packs: packs, UploadBytes: uploadBytes,
		Pulled: pulled, Deleted: deleted, Conflicts: conflicts,
		Skipped: res.Skipped,
	}, nil
}

func planFor(local, remote, base *store.Manifest, opt Options) ([]planItem, error) {
	baseRepo := loadBaseRepoID(opt.BasePath)
	if opt.StoreRepoID != "" && baseRepo != "" && baseRepo != opt.StoreRepoID && !opt.Recover {
		return nil, ErrDifferentStore
	}
	if opt.Recover {
		return buildRecoveryPlan(local, remote), nil
	}
	return buildPlan(local, remote, base, conflictPolicy(opt.Conflict)), nil
}

// Preview scans and plans a run without writing anything.
func Preview(ctx context.Context, opt Options) (PreviewResult, error) {
	st, err := store.Open(opt.StoreRoot)
	if err != nil {
		return PreviewResult{}, err
	}
	res, err := scanRepo(ctx, opt, func(Progress) {})
	if err != nil {
		return PreviewResult{}, err
	}
	if opt.StoreRepoID == "" {
		opt.StoreRepoID = st.Marker().RepoID
	}
	remote := &store.Manifest{}
	latest, _, err := st.Latest()
	switch {
	case errors.Is(err, store.ErrNoHead):
	case err != nil:
		return PreviewResult{}, err
	default:
		remote = latest
	}
	plan, err := planFor(&res.Manifest, remote, loadBase(opt.BasePath), opt)
	if err != nil {
		return PreviewResult{}, err
	}
	return summarizePlan(plan), nil
}

func summarizePlan(plan []planItem) PreviewResult {
	var p PreviewResult
	for _, item := range plan {
		switch item.Kind {
		case actPush:
			p.Push++
		case actPull:
			p.Pull++
		case actDeleteLocal, actDeleteRemote:
			p.Deletes++
		case actConflictRemote, actConflictLocal:
			p.Conflicts++
		}
	}
	p.Total = len(plan)
	return p
}

// buildRecoveryPlan merges two trees without a shared ancestor. It never
// deletes: local-only files are pushed, store-only files are pulled, and
// differing files keep both variants as a conflict copy.
func buildRecoveryPlan(local, remote *store.Manifest) []planItem {
	lm := entryMap(local)
	rm := entryMap(remote)
	seen := make(map[string]struct{}, len(lm)+len(rm))
	for p := range lm {
		seen[p] = struct{}{}
	}
	for p := range rm {
		seen[p] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var plan []planItem
	for _, p := range paths {
		l, lok := lm[p]
		s, sok := rm[p]
		switch {
		case lok && !sok:
			plan = append(plan, planItem{Path: p, Kind: actPush, Local: l})
		case !lok && sok:
			plan = append(plan, planItem{Path: p, Kind: actPull, Remote: s})
		case !lok && !sok:
			continue
		case sameEntry(optEntry{l, true}, optEntry{s, true}):
			continue
		case l.Type == store.TypeDir && s.Type == store.TypeDir:
			continue
		case l.Type == store.TypeDir || s.Type == store.TypeDir:
			// A file/directory type conflict: keep the local variant and report
			// it; there is no safe automatic replacement.
			plan = append(plan, planItem{Path: p, Kind: actPush, Local: l})
		default:
			plan = append(plan, planItem{Path: p, Kind: actConflictRemote, Local: l, Remote: s})
		}
	}
	return plan
}

func buildPlan(local, remote, base *store.Manifest, policy string) []planItem {
	lm := entryMap(local)
	rm := entryMap(remote)
	bm := entryMap(base)

	seen := make(map[string]struct{}, len(lm)+len(rm)+len(bm))
	for p := range lm {
		seen[p] = struct{}{}
	}
	for p := range rm {
		seen[p] = struct{}{}
	}
	for p := range bm {
		seen[p] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var plan []planItem
	for _, p := range paths {
		l := optEntry{lm[p], false}
		if e, ok := lm[p]; ok {
			l = optEntry{e, true}
		}
		s := optEntry{rm[p], false}
		if e, ok := rm[p]; ok {
			s = optEntry{e, true}
		}
		b := optEntry{bm[p], false}
		if e, ok := bm[p]; ok {
			b = optEntry{e, true}
		}

		localChanged := !sameEntry(l, b)
		remoteChanged := !sameEntry(s, b)
		switch {
		case !localChanged && !remoteChanged:
			continue
		case localChanged && !remoteChanged:
			if l.isNil() {
				plan = append(plan, planItem{Path: p, Kind: actDeleteRemote})
			} else {
				plan = append(plan, planItem{Path: p, Kind: actPush, Local: l.e})
			}
		case !localChanged && remoteChanged:
			if s.isNil() {
				plan = append(plan, planItem{Path: p, Kind: actDeleteLocal})
			} else {
				plan = append(plan, planItem{Path: p, Kind: actPull, Remote: s.e})
			}
		default:
			if sameEntry(l, s) {
				continue
			}
			if l.isNil() {
				// Local deletion, remote modification: keep the modification.
				plan = append(plan, planItem{Path: p, Kind: actPull, Remote: s.e})
				continue
			}
			if s.isNil() {
				// Remote deletion, local modification: keep the modification.
				plan = append(plan, planItem{Path: p, Kind: actPush, Local: l.e})
				continue
			}
			if l.e.Type == store.TypeDir || s.e.Type == store.TypeDir {
				plan = append(plan, planItem{Path: p, Kind: actPush, Local: l.e})
				continue
			}
			kind := actConflictRemote
			switch policy {
			case ConflictLocalWins:
				kind = actConflictLocal
			case ConflictNewest:
				if s.e.MtimeNS > l.e.MtimeNS {
					kind = actConflictRemote
				} else {
					kind = actConflictLocal
				}
			}
			plan = append(plan, planItem{Path: p, Kind: kind, Local: l.e, Remote: s.e})
		}
	}
	return plan
}

// ensureParents keeps the manifest a full tree even when a path survived
// without its parent directory entry.
func ensureParents(target map[string]store.Entry) {
	for p := range target {
		for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := target[dir]; !ok {
				target[dir] = store.Entry{Path: dir, Type: store.TypeDir, Mode: 0o755}
			}
		}
	}
}

func conflictPolicy(p string) string {
	switch p {
	case ConflictLocalWins, ConflictNewest:
		return p
	default:
		return ConflictCopy
	}
}

func planSizes(plan []planItem) (pull, push int64) {
	for _, item := range plan {
		switch item.Kind {
		case actPull, actConflictRemote:
			pull += item.Remote.Size
		case actPush, actConflictLocal:
			push += item.Local.Size
		}
	}
	return pull, push
}

func checkSpace(path string, needed int64) error {
	if needed <= 0 {
		return nil
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return fmt.Errorf("statfs %s: %w", path, err)
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	const margin = 16 << 20
	if free < needed+margin {
		return fmt.Errorf("not enough free space on %s: need %d MiB, have %d MiB",
			path, (needed+margin)>>20, free>>20)
	}
	return nil
}

func applyRemote(st *store.Store, repo string, e store.Entry) error {
	dst := filepath.Join(repo, filepath.FromSlash(e.Path))
	switch e.Type {
	case store.TypeDir:
		if fi, err := os.Lstat(dst); err == nil && !fi.IsDir() {
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("replace %q: %w", e.Path, err)
			}
		}
		if err := os.MkdirAll(dst, store.UnixToMode(e.Mode)|0o700); err != nil {
			return fmt.Errorf("create directory %q: %w", e.Path, err)
		}
		return nil
	case store.TypeSymlink:
		if err := prepareTarget(dst); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + ".walky-tmp-link"
		_ = os.Remove(tmp)
		if err := os.Symlink(e.Target, tmp); err != nil {
			return fmt.Errorf("create symlink %q: %w", e.Path, err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("install symlink %q: %w", e.Path, err)
		}
		return nil
	case store.TypeFile:
		return applyFile(st, dst, e)
	default:
		return nil
	}
}

func prepareTarget(dst string) error {
	fi, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("refusing to replace directory %s with a file", dst)
	}
	return os.Remove(dst)
}

func applyFile(st *store.Store, dst string, e store.Entry) error {
	if err := prepareTarget(dst); err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".walky-apply-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	obj, err := st.OpenObject(e.Hash)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("open object for %q: %w", e.Path, err)
	}
	defer obj.Close()

	h := blake3.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), obj)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy %q: %w", e.Path, err)
	}
	if n != e.Size {
		_ = tmp.Close()
		return fmt.Errorf("%q: size mismatch: manifest %d, object %d", e.Path, e.Size, n)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.Hash {
		_ = tmp.Close()
		return fmt.Errorf("%q: object %s is corrupt", e.Path, e.Hash)
	}
	if err := tmp.Chmod(store.UnixToMode(e.Mode)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("install %q: %w", e.Path, err)
	}
	renamed = true
	if e.MtimeNS > 0 {
		t := time.Unix(0, e.MtimeNS)
		if err := os.Chtimes(dst, t, t); err != nil {
			return err
		}
	}
	return nil
}

func resolveConflictCopy(st *store.Store, repo string, item planItem, host string, now time.Time) (string, store.Entry, error) {
	localPath := filepath.Join(repo, filepath.FromSlash(item.Path))
	rel := uniqueConflictRel(repo, item.Path, host, now)
	dst := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", store.Entry{}, err
	}
	if err := os.Rename(localPath, dst); err != nil {
		return "", store.Entry{}, fmt.Errorf("rename conflict copy %q: %w", item.Path, err)
	}
	if err := applyRemote(st, repo, item.Remote); err != nil {
		return "", store.Entry{}, err
	}
	entry := item.Local
	entry.Path = rel
	return rel, entry, nil
}

func uniqueConflictRel(repo, rel, host string, now time.Time) string {
	dir, base := filepath.Split(rel)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	name := fmt.Sprintf("%s.conflict-%s-%s", stem, host, now.Format("20060102-150405"))
	candidate := filepath.ToSlash(filepath.Join(dir, name+ext))
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(candidate))); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
		candidate = filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("%s-%d%s", name, i, ext)))
	}
}

func sanitizeHost(host string) string {
	if host == "" {
		return "host"
	}
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func moveToTrash(repo, trashRoot, rel string, now time.Time) error {
	src := filepath.Join(repo, filepath.FromSlash(rel))
	if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if trashRoot == "" {
		return fmt.Errorf("trash directory is not configured")
	}
	dst := filepath.Join(trashRoot, now.Format("20060102-150405"), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move %q to trash: %w", rel, err)
	}
	return nil
}

func pruneEmptyDirs(repo string, deleted map[string]bool) {
	if len(deleted) == 0 {
		return
	}
	dirs := make([]string, 0, len(deleted))
	for p := range deleted {
		dirs = append(dirs, p)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, p := range dirs {
		_ = os.Remove(filepath.Join(repo, filepath.FromSlash(p)))
	}
}

func applyDirMetadata(repo string, remote *store.Manifest) error {
	var dirs []store.Entry
	for _, e := range remote.Entries {
		if e.Type == store.TypeDir {
			dirs = append(dirs, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Path > dirs[j].Path })
	for _, e := range dirs {
		dst := filepath.Join(repo, filepath.FromSlash(e.Path))
		if _, err := os.Stat(dst); err != nil {
			continue
		}
		if e.Mode != 0 {
			if err := os.Chmod(dst, store.UnixToMode(e.Mode)); err != nil {
				return err
			}
		}
		if e.MtimeNS > 0 {
			t := time.Unix(0, e.MtimeNS)
			if err := os.Chtimes(dst, t, t); err != nil {
				return err
			}
		}
	}
	return nil
}

func removedEntries(remote *store.Manifest, target map[string]store.Entry) []store.Entry {
	var out []store.Entry
	for _, e := range remote.Entries {
		if _, ok := target[e.Path]; !ok {
			out = append(out, e)
		}
	}
	return out
}

func loadBase(path string) *store.Manifest {
	if path == "" {
		return &store.Manifest{}
	}
	m, err := store.ReadManifestFile(path)
	if err != nil {
		return &store.Manifest{}
	}
	return m
}

func loadBaseRepoID(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path + ".meta.json")
	if err != nil {
		return ""
	}
	var meta struct {
		RepoID string `json:"repo_id"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return meta.RepoID
}

func saveBase(path string, gen int64, m *store.Manifest, machine, repoID string) error {
	if path == "" {
		return nil
	}
	if err := store.WriteManifestFile(path, m); err != nil {
		return err
	}
	meta := map[string]any{
		"gen":      gen,
		"machine":  machine,
		"saved_at": time.Now().UTC(),
		"repo_id":  repoID,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path+".meta.json")
}
