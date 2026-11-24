package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func day(n int) time.Time {
	return time.Date(2026, 1, n, 12, 0, 0, 0, time.UTC)
}

func commitTree(t *testing.T, st *Store, created time.Time, files map[string]string) int64 {
	t.Helper()
	m := &Manifest{}
	for path, content := range files {
		hash, size, err := st.PutObject(strings.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		m.Entries = append(m.Entries, Entry{
			Path: path, Type: TypeFile, Size: size, Mode: 0o644, Hash: hash,
		})
	}
	res, err := st.Commit(m, CommitOptions{Created: created})
	if err != nil {
		t.Fatal(err)
	}
	return res.Gen
}

func objectHash(t *testing.T, st *Store, content string) string {
	t.Helper()
	hash, _, err := st.PutObject(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestGCRetentionAndObjects(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hashA := objectHash(t, st, "alpha")
	gen1 := commitTree(t, st, day(1), map[string]string{"a": "alpha"})
	gen2 := commitTree(t, st, day(2), map[string]string{"b": "beta"})
	gen3 := commitTree(t, st, day(3), map[string]string{"c": "gamma"})

	rep, err := st.GC(GCOptions{KeepDaily: 1, TrashDays: -1})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if len(rep.KeptGenerations) != 1 || rep.KeptGenerations[0] != gen3 {
		t.Fatalf("kept = %v, want [%d]", rep.KeptGenerations, gen3)
	}
	if len(rep.RemovedGenerations) != 2 {
		t.Fatalf("removed = %v", rep.RemovedGenerations)
	}
	if _, _, err := st.ReadSnapshot(gen1); err == nil {
		t.Fatal("generation 1 survived GC")
	}
	if ok, _ := st.HasObject(hashA); ok {
		t.Fatal("unreferenced object survived GC")
	}
	if ok, _ := st.HasObject(objectHash(t, st, "gamma")); !ok {
		t.Fatal("live object was collected")
	}
	if rep.RemovedObjects < 2 {
		t.Fatalf("removed objects = %d", rep.RemovedObjects)
	}
	verifyOK(t, st)
	_ = gen2
}

func TestGCMachineBaseIsKept(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hashA := objectHash(t, st, "alpha")
	gen1 := commitTree(t, st, day(1), map[string]string{"a": "alpha"})
	commitTree(t, st, day(2), map[string]string{"b": "beta"})
	gen3 := commitTree(t, st, day(3), map[string]string{"c": "gamma"})

	if err := st.SaveMachine(Machine{ID: "m1", BaseGen: gen1, LastSync: day(1)}); err != nil {
		t.Fatal(err)
	}
	rep, err := st.GC(GCOptions{KeepDaily: 1, TrashDays: -1})
	if err != nil {
		t.Fatal(err)
	}
	kept := map[int64]bool{}
	for _, g := range rep.KeptGenerations {
		kept[g] = true
	}
	if !kept[gen1] || !kept[gen3] {
		t.Fatalf("kept = %v, want machine base %d and HEAD %d", rep.KeptGenerations, gen1, gen3)
	}
	if _, _, err := st.ReadSnapshot(gen1); err != nil {
		t.Fatalf("machine base manifest was removed: %v", err)
	}
	if ok, _ := st.HasObject(hashA); !ok {
		t.Fatal("object referenced by a machine base was removed")
	}
	verifyOK(t, st)
}

func TestGCPackCompaction(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	contentA, contentB := "alpha", "beta"
	hashA, hashB := hashBytes([]byte(contentA)), hashBytes([]byte(contentB))
	packer := st.NewPacker(DefaultPackOptions())
	if err := packer.Add(hashA, int64(len(contentA)), strings.NewReader(contentA)); err != nil {
		t.Fatal(err)
	}
	if err := packer.Add(hashB, int64(len(contentB)), strings.NewReader(contentB)); err != nil {
		t.Fatal(err)
	}
	if err := packer.Close(); err != nil {
		t.Fatal(err)
	}
	commitEntries(t, st, day(1), []Entry{
		{Path: "a", Type: TypeFile, Size: int64(len(contentA)), Mode: 0o644, Hash: hashA},
		{Path: "b", Type: TypeFile, Size: int64(len(contentB)), Mode: 0o644, Hash: hashB},
	})
	commitEntries(t, st, day(2), []Entry{
		{Path: "a", Type: TypeFile, Size: int64(len(contentA)), Mode: 0o644, Hash: hashA},
	})

	rep, err := st.GC(GCOptions{KeepDaily: 1, TrashDays: -1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CompactedPacks != 1 || rep.RemovedPacks != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if ok, _ := st.HasObject(hashA); !ok {
		t.Fatal("live packed object disappeared")
	}
	if ok, _ := st.HasObject(hashB); ok {
		t.Fatal("dead packed object survived")
	}
	files, err := filepath.Glob(filepath.Join(st.Root(), "packs", "*.pack"))
	if err != nil || len(files) != 1 {
		t.Fatalf("packs = %v, %v", files, err)
	}
	verifyOK(t, st)
}

func TestGCPackDuplicateWithStandalone(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	contentA, contentB := "alpha", "beta"
	hashA := objectHash(t, st, contentA)
	hashB := objectHash(t, st, contentB)
	packer := st.NewPacker(DefaultPackOptions())
	if err := packer.Add(hashA, int64(len(contentA)), strings.NewReader(contentA)); err != nil {
		t.Fatal(err)
	}
	if err := packer.Add(hashB, int64(len(contentB)), strings.NewReader(contentB)); err != nil {
		t.Fatal(err)
	}
	if err := packer.Close(); err != nil {
		t.Fatal(err)
	}
	commitEntries(t, st, day(1), []Entry{
		{Path: "a", Type: TypeFile, Size: int64(len(contentA)), Mode: 0o644, Hash: hashA},
		{Path: "b", Type: TypeFile, Size: int64(len(contentB)), Mode: 0o644, Hash: hashB},
	})
	commitEntries(t, st, day(2), []Entry{
		{Path: "a", Type: TypeFile, Size: int64(len(contentA)), Mode: 0o644, Hash: hashA},
	})

	rep, err := st.GC(GCOptions{KeepDaily: 1, TrashDays: -1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.RemovedPacks != 1 {
		t.Fatalf("removed packs = %d, want 1", rep.RemovedPacks)
	}
	if ok, _ := st.HasObject(hashA); !ok {
		t.Fatal("live object disappeared")
	}
	files, _ := filepath.Glob(filepath.Join(st.Root(), "packs", "*.pack"))
	if len(files) != 0 {
		t.Fatalf("packs left: %v", files)
	}
	verifyOK(t, st)
}

func commitEntries(t *testing.T, st *Store, created time.Time, entries []Entry) int64 {
	t.Helper()
	res, err := st.Commit(&Manifest{Entries: entries}, CommitOptions{Created: created})
	if err != nil {
		t.Fatal(err)
	}
	return res.Gen
}

func TestGCDryRun(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hashA := objectHash(t, st, "alpha")
	gen1 := commitTree(t, st, day(1), map[string]string{"a": "alpha"})
	commitTree(t, st, day(2), map[string]string{"b": "beta"})

	rep, err := st.GC(GCOptions{KeepDaily: 1, TrashDays: -1, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || len(rep.RemovedGenerations) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if _, _, err := st.ReadSnapshot(gen1); err != nil {
		t.Fatalf("dry run removed a generation: %v", err)
	}
	if ok, _ := st.HasObject(hashA); !ok {
		t.Fatal("dry run removed an object")
	}
}

func TestGCTrashExpiry(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hashA := objectHash(t, st, "alpha")
	commitTree(t, st, day(1), map[string]string{"a": "alpha"})
	gen2 := commitTree(t, st, day(2), map[string]string{"b": "beta"})

	if err := st.SaveTrash(gen2, []Entry{{
		Path: "a", Type: TypeFile, Size: 5, Mode: 0o644, Hash: hashA,
	}}); err != nil {
		t.Fatal(err)
	}
	trashFile := filepath.Join(st.Root(), "trash", "00000002.json")
	old := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(trashFile, old, old); err != nil {
		t.Fatal(err)
	}

	rep, err := st.GC(GCOptions{KeepDaily: 1, TrashDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ExpiredTrash != 1 {
		t.Fatalf("expired trash = %d", rep.ExpiredTrash)
	}
	if _, err := os.Stat(trashFile); !os.IsNotExist(err) {
		t.Fatal("expired trash file still exists")
	}
	if ok, _ := st.HasObject(hashA); ok {
		t.Fatal("object held only by expired trash survived")
	}
	verifyOK(t, st)
}

func TestGCWithoutHead(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GC(GCOptions{KeepDaily: 1}); err == nil {
		t.Fatal("GC succeeded without a committed snapshot")
	}
}

func TestGC100Generations(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	var lastHash string
	for i := 1; i <= 100; i++ {
		content := strings.Repeat("x", i)
		lastHash = objectHash(t, st, content)
		commitTree(t, st, day(1).Add(time.Duration(i)*24*time.Hour), map[string]string{"f": content})
	}
	rep, err := st.GC(GCOptions{KeepDaily: 14, KeepWeekly: 0, KeepMonthly: 0, TrashDays: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.KeptGenerations) != 14 {
		t.Fatalf("kept %d generations, want 14: %v", len(rep.KeptGenerations), rep.KeptGenerations)
	}
	if ok, _ := st.HasObject(lastHash); !ok {
		t.Fatal("HEAD object was collected")
	}
	verifyOK(t, st)
}

func verifyOK(t *testing.T, st *Store) {
	t.Helper()
	rep, err := st.Verify(true)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("store is corrupt after GC: %+v", rep)
	}
}
