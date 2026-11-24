package store

import (
	"bytes"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/blake3"
)

func hashBytes(b []byte) string {
	h := blake3.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestPackRoundTrip(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("hello packed world")
	hash := hashBytes(content)

	p := st.NewPacker(DefaultPackOptions())
	if err := p.Add(hash, int64(len(content)), bytes.NewReader(content)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if p.Packs() != 1 {
		t.Fatalf("packs = %d, want 1", p.Packs())
	}

	ok, err := st.HasObject(hash)
	if err != nil || !ok {
		t.Fatalf("HasObject = %v, %v", ok, err)
	}
	size, err := st.ObjectInfo(hash)
	if err != nil || size != int64(len(content)) {
		t.Fatalf("ObjectInfo = %d, %v", size, err)
	}
	rc, err := st.OpenObject(hash)
	if err != nil {
		t.Fatalf("OpenObject: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content = %q, %v", got, err)
	}
	if err := st.CheckObject(hash); err != nil {
		t.Fatalf("CheckObject: %v", err)
	}
}

func TestPackRejectsChangedContent(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	wrong := hashBytes([]byte("other"))
	p := st.NewPacker(DefaultPackOptions())
	if err := p.Add(wrong, 5, bytes.NewReader([]byte("hello"))); err == nil {
		t.Fatal("Add accepted a hash mismatch")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Packs() != 0 {
		t.Fatalf("packs = %d, want 0 after rollback", p.Packs())
	}
	if ok, _ := st.HasObject(wrong); ok {
		t.Fatal("rolled-back object is visible")
	}
}

func TestPackCommitRestore(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	small := []byte("small file in a pack")
	smallHash := hashBytes(small)
	large := bytes.Repeat([]byte("L"), int(PackThreshold)+17)
	largeHash := hashBytes(large)

	p := st.NewPacker(DefaultPackOptions())
	if err := p.Add(smallHash, int64(len(small)), bytes.NewReader(small)); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PutObject(bytes.NewReader(large)); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{Entries: []Entry{
		{Path: "large.bin", Type: TypeFile, Size: int64(len(large)), Mode: 0o644, Hash: largeHash},
		{Path: "small.txt", Type: TypeFile, Size: int64(len(small)), Mode: 0o644, Hash: smallHash},
	}}
	if _, err := st.Commit(m, CommitOptions{}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	rep, err := st.Verify(true)
	if err != nil || !rep.OK() {
		t.Fatalf("verify = %+v, %v", rep, err)
	}

	dest := t.TempDir()
	if err := st.Restore(dest, m, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "small.txt"))
	if err != nil || !bytes.Equal(got, small) {
		t.Fatalf("small.txt = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dest, "large.bin"))
	if err != nil || !bytes.Equal(got, large) {
		t.Fatalf("large.bin mismatch: %v", err)
	}
}

func TestVerifyDetectsPackedCorruption(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("packed payload")
	hash := hashBytes(content)
	p := st.NewPacker(DefaultPackOptions())
	if err := p.Add(hash, int64(len(content)), bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Entries: []Entry{{
		Path: "f.txt", Type: TypeFile, Size: int64(len(content)), Mode: 0o644, Hash: hash,
	}}}
	if _, err := st.Commit(m, CommitOptions{}); err != nil {
		t.Fatal(err)
	}

	packs, err := filepath.Glob(filepath.Join(st.Root(), packsDir, "*.pack"))
	if err != nil || len(packs) != 1 {
		t.Fatalf("packs = %v, %v", packs, err)
	}
	data, err := os.ReadFile(packs[0])
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-3] ^= 0xff // corrupt the zstd frame
	if err := os.WriteFile(packs[0], data, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := st.Verify(true)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.OK() || len(rep.Corrupt) != 1 {
		t.Fatalf("packed corruption not detected: %+v", rep)
	}
}

func TestPackIndexRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if _, err := readPackIndex(filepath.Join(dir, "missing.idx")); err == nil {
		t.Fatal("missing index accepted")
	}
	bad := filepath.Join(dir, "bad.idx")
	if err := os.WriteFile(bad, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackIndex(bad); err == nil {
		t.Fatal("bad magic accepted")
	}
	// Valid magic, truncated body.
	if err := os.WriteFile(bad, []byte("WPK1\x01\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackIndex(bad); err == nil {
		t.Fatal("truncated index accepted")
	}
}

func TestPutObjectReplacesIncompleteObject(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("durable content")
	hash := hashBytes(content)
	p, err := st.ObjectPath(hash)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an object left truncated by a crash: wrong size, same name.
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.PutObject(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if got != hash {
		t.Fatalf("hash = %s, want %s", got, hash)
	}
	if err := st.CheckObject(hash); err != nil {
		t.Fatalf("object was not replaced: %v", err)
	}
}

func TestCommittedHashes(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.CommittedHashes()
	if err != nil || len(empty) != 0 {
		t.Fatalf("no head: %v, %v", empty, err)
	}

	content := []byte("committed")
	hash, size, err := st.PutObject(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	// Present but not committed yet: must not be trusted.
	set, err := st.CommittedHashes()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set[hash]; ok {
		t.Fatal("uncommitted object reported as durable")
	}

	m := &Manifest{Entries: []Entry{{
		Path: "f", Type: TypeFile, Size: size, Mode: 0o644, Hash: hash,
	}}}
	if _, err := st.Commit(m, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	set, err = st.CommittedHashes()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set[hash]; !ok {
		t.Fatal("committed object missing from durable set")
	}
}
