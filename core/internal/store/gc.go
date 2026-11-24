package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// GCOptions controls retention and garbage collection.
type GCOptions struct {
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	TrashDays   int
	DryRun      bool
	// Now overrides the clock (tests).
	Now time.Time
}

// GCReport summarizes a collection run.
type GCReport struct {
	DryRun             bool
	KeptGenerations    []int64
	RemovedGenerations []int64
	RemovedObjects     int
	RemovedPacks       int
	CompactedPacks     int
	ExpiredTrash       int
	FreedBytes         int64
	Warnings           []string
}

type snapshotInfo struct {
	gen     int64
	created time.Time
}

// GC applies the retention policy, removes unreachable objects and compacts
// packs that mix live and dead entries. HEAD is never removed.
func (s *Store) GC(opts GCOptions) (*GCReport, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rep := &GCReport{DryRun: opts.DryRun}

	head, err := s.Head()
	if err != nil {
		return nil, fmt.Errorf("gc requires committed snapshots: %w", err)
	}

	snaps, err := s.snapshotInfos()
	if err != nil {
		return nil, err
	}
	keep := map[int64]struct{}{head.Gen: {}}
	s.keepMachineBases(keep, rep)
	selectRetention(keep, snaps, opts)

	gens := make([]int64, 0, len(keep))
	for gen := range keep {
		gens = append(gens, gen)
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i] < gens[j] })
	rep.KeptGenerations = gens

	live := make(map[string]struct{})
	for _, gen := range gens {
		m, _, err := s.ReadSnapshot(gen)
		if err != nil {
			return nil, fmt.Errorf("read kept generation %d: %w", gen, err)
		}
		for _, e := range m.Entries {
			if e.Type == TypeFile {
				live[e.Hash] = struct{}{}
			}
		}
	}

	trashKeep, trashExpired, err := s.trashEntries(now, opts.TrashDays)
	if err != nil {
		return nil, err
	}
	for _, entries := range trashKeep {
		for _, e := range entries {
			if e.Type == TypeFile {
				live[e.Hash] = struct{}{}
			}
		}
	}

	standalone, err := s.sweepObjects(live, rep)
	if err != nil {
		return nil, err
	}
	if err := s.sweepPacks(live, standalone, rep); err != nil {
		return nil, err
	}

	// Generation files go last: objects are already collected.
	for _, sn := range snaps {
		if _, ok := keep[sn.gen]; ok {
			continue
		}
		rep.RemovedGenerations = append(rep.RemovedGenerations, sn.gen)
		if opts.DryRun {
			continue
		}
		for _, p := range []string{s.snapshotPath(sn.gen), s.metaPath(sn.gen)} {
			if fi, err := os.Stat(p); err == nil {
				rep.FreedBytes += fi.Size()
				if err := os.Remove(p); err != nil {
					return nil, err
				}
			}
		}
	}

	for _, path := range trashExpired {
		rep.ExpiredTrash++
		if opts.DryRun {
			continue
		}
		if fi, err := os.Stat(path); err == nil {
			rep.FreedBytes += fi.Size()
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	if !opts.DryRun {
		if err := s.Sync(); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

func (s *Store) snapshotInfos() ([]snapshotInfo, error) {
	metas, err := s.snapshotMetas()
	if err != nil {
		return nil, err
	}
	out := make([]snapshotInfo, 0, len(metas))
	for _, m := range metas {
		out = append(out, snapshotInfo{gen: m.Gen, created: m.Created})
	}
	return out, nil
}

// keepMachineBases retains the generation each machine last synced from, so a
// machine that returns after a long absence can still merge against its base.
func (s *Store) keepMachineBases(keep map[int64]struct{}, rep *GCReport) {
	entries, err := os.ReadDir(filepath.Join(s.root, machinesDir))
	if err != nil {
		if !os.IsNotExist(err) {
			rep.Warnings = append(rep.Warnings, "cannot list machines: "+err.Error())
		}
		return
	}
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, machinesDir, de.Name()))
		if err != nil {
			rep.Warnings = append(rep.Warnings, "cannot read "+de.Name()+": "+err.Error())
			continue
		}
		var m Machine
		if err := json.Unmarshal(data, &m); err != nil {
			rep.Warnings = append(rep.Warnings, "cannot parse "+de.Name()+": "+err.Error())
			continue
		}
		if m.BaseGen <= 0 {
			continue
		}
		if _, err := os.Stat(s.metaPath(m.BaseGen)); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf(
				"machine %s has a stale base generation %d", m.ID, m.BaseGen))
			continue
		}
		keep[m.BaseGen] = struct{}{}
	}
}

func selectRetention(keep map[int64]struct{}, snaps []snapshotInfo, opts GCOptions) {
	days := map[string]bool{}
	weeks := map[string]bool{}
	months := map[string]bool{}
	daily, weekly, monthly := 0, 0, 0
	for _, sn := range snaps {
		if opts.KeepDaily > 0 {
			key := sn.created.Format("2006-01-02")
			if !days[key] && daily < opts.KeepDaily {
				days[key] = true
				daily++
				keep[sn.gen] = struct{}{}
			}
		}
		if opts.KeepWeekly > 0 {
			y, w := sn.created.ISOWeek()
			key := fmt.Sprintf("%04d-W%02d", y, w)
			if !weeks[key] && weekly < opts.KeepWeekly {
				weeks[key] = true
				weekly++
				keep[sn.gen] = struct{}{}
			}
		}
		if opts.KeepMonthly > 0 {
			key := sn.created.Format("2006-01")
			if !months[key] && monthly < opts.KeepMonthly {
				months[key] = true
				monthly++
				keep[sn.gen] = struct{}{}
			}
		}
	}
}

func (s *Store) trashEntries(now time.Time, days int) (keep [][]Entry, expired []string, err error) {
	dir := filepath.Join(s.root, trashDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, de.Name())
		info, err := de.Info()
		if err != nil {
			return nil, nil, err
		}
		if days >= 0 && now.Sub(info.ModTime()) > time.Duration(days)*24*time.Hour {
			expired = append(expired, path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		var list []Entry
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, nil, fmt.Errorf("parse trash %s: %w", de.Name(), err)
		}
		keep = append(keep, list)
	}
	return keep, expired, nil
}

func (s *Store) sweepObjects(live map[string]struct{}, rep *GCReport) (map[string]bool, error) {
	standalone := make(map[string]bool)
	root := filepath.Join(s.root, objectsDir)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".tmp-") {
			if !rep.DryRun && time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.Remove(path)
			}
			return nil
		}
		if _, ok := live[name]; ok {
			standalone[name] = true
			return nil
		}
		rep.RemovedObjects++
		rep.FreedBytes += info.Size()
		if rep.DryRun {
			return nil
		}
		return os.Remove(path)
	})
	return standalone, err
}

type packCopy struct {
	pack int
	loc  packLoc
}

type mixedPack struct {
	pack   int
	copies []packCopy
	bytes  int64
}

func (s *Store) sweepPacks(live map[string]struct{}, standalone map[string]bool, rep *GCReport) error {
	cat, err := s.catalog()
	if err != nil {
		return err
	}
	perPack := make([][]packLoc, len(cat.packs))
	for _, loc := range cat.locs {
		perPack[loc.pack] = append(perPack[loc.pack], loc)
	}

	retained := make(map[string]struct{})
	var dead []int
	var mixed []mixedPack
	copied := make(map[string]struct{})
	var copyBytes int64

	for i := range cat.packs {
		liveCount := 0
		for _, loc := range perPack[i] {
			if _, ok := live[hex.EncodeToString(loc.hash[:])]; ok {
				liveCount++
			}
		}
		switch {
		case liveCount == 0:
			dead = append(dead, i)
		case liveCount == len(perPack[i]):
			for _, loc := range perPack[i] {
				retained[hex.EncodeToString(loc.hash[:])] = struct{}{}
			}
		default:
			mp := mixedPack{pack: i}
			for _, loc := range perPack[i] {
				hash := hex.EncodeToString(loc.hash[:])
				if _, ok := live[hash]; !ok {
					continue
				}
				if standalone[hash] {
					continue
				}
				if _, ok := retained[hash]; ok {
					continue
				}
				if _, ok := copied[hash]; ok {
					continue
				}
				copied[hash] = struct{}{}
				mp.copies = append(mp.copies, packCopy{pack: i, loc: loc})
				mp.bytes += int64(loc.rawLen)
			}
			copyBytes += mp.bytes
			mixed = append(mixed, mp)
		}
	}

	canCompact := true
	if copyBytes > 0 {
		free, err := diskFree(s.root)
		if err != nil {
			return err
		}
		const margin = 16 << 20
		if free < copyBytes+margin {
			canCompact = false
			rep.Warnings = append(rep.Warnings,
				fmt.Sprintf("pack compaction skipped: need %d MiB, have %d MiB",
					(copyBytes+margin)>>20, free>>20))
		}
	}

	var deletable []int
	var toCopy []mixedPack
	for _, mp := range mixed {
		if canCompact || len(mp.copies) == 0 {
			deletable = append(deletable, mp.pack)
			if canCompact {
				toCopy = append(toCopy, mp)
			}
		}
	}

	if !rep.DryRun && len(toCopy) > 0 {
		packer := s.NewPacker(DefaultPackOptions())
		for _, mp := range toCopy {
			for _, c := range mp.copies {
				data, err := s.readPackedObject(cat.packs[c.pack], c.loc)
				if err != nil {
					_ = packer.Close()
					return err
				}
				hash := hex.EncodeToString(c.loc.hash[:])
				if err := packer.Add(hash, int64(len(data)), bytes.NewReader(data)); err != nil {
					_ = packer.Close()
					return fmt.Errorf("compact %s: %w", hash, err)
				}
			}
		}
		if err := packer.Close(); err != nil {
			return err
		}
	}
	rep.CompactedPacks += len(deletable)

	deletePacks := make([]string, 0, len(dead)+len(deletable))
	for _, i := range dead {
		deletePacks = append(deletePacks, cat.packs[i])
	}
	for _, i := range deletable {
		deletePacks = append(deletePacks, cat.packs[i])
	}
	if rep.DryRun {
		rep.RemovedPacks += len(deletePacks)
		return nil
	}
	for _, name := range deletePacks {
		packPath := filepath.Join(s.root, packsDir, name)
		idxPath := filepath.Join(s.root, packsDir, strings.TrimSuffix(name, ".pack")+".idx")
		if fi, err := os.Stat(packPath); err == nil {
			rep.FreedBytes += fi.Size()
		}
		if fi, err := os.Stat(idxPath); err == nil {
			rep.FreedBytes += fi.Size()
		}
		if err := os.Remove(packPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(idxPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		rep.RemovedPacks++
	}
	s.invalidateCatalog()
	return nil
}

func (s *Store) readPackedObject(packName string, loc packLoc) ([]byte, error) {
	f, err := os.Open(filepath.Join(s.root, packsDir, packName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(io.NewSectionReader(f, loc.off, int64(loc.compLen)))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, int64(loc.rawLen)+1))
	if err != nil {
		return nil, err
	}
	if len(data) != int(loc.rawLen) {
		return nil, fmt.Errorf("packed object size mismatch in %s", packName)
	}
	return data, nil
}

func diskFree(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
