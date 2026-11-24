package store

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestCommitFailpoints(t *testing.T) {
	st, err := Init(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	hashA, sizeA, err := st.PutObject(bytes.NewReader([]byte("alpha")))
	if err != nil {
		t.Fatal(err)
	}
	m1 := &Manifest{Entries: []Entry{{
		Path: "a.txt", Type: TypeFile, Size: sizeA, Mode: 0o644, Hash: hashA,
	}}}
	if _, err := st.Commit(m1, CommitOptions{Machine: "m"}); err != nil {
		t.Fatal(err)
	}

	hashB, sizeB, err := st.PutObject(bytes.NewReader([]byte("beta")))
	if err != nil {
		t.Fatal(err)
	}
	m2 := &Manifest{Entries: []Entry{
		{Path: "a.txt", Type: TypeFile, Size: sizeA, Mode: 0o644, Hash: hashA},
		{Path: "b.txt", Type: TypeFile, Size: sizeB, Mode: 0o644, Hash: hashB},
	}}

	for _, failAt := range []string{"sync", "snapshot", "meta", "head"} {
		t.Run(failAt, func(t *testing.T) {
			injected := errors.New("injected failure")
			testHook = func(step string) error {
				if step == failAt {
					return injected
				}
				return nil
			}
			_, err := st.Commit(m2, CommitOptions{Machine: "m"})
			testHook = nil
			if !errors.Is(err, injected) {
				t.Fatalf("commit = %v, want injected failure", err)
			}
			head, err := st.Head()
			if err != nil {
				t.Fatalf("head after failed commit: %v", err)
			}
			if head.Gen != 1 {
				t.Fatalf("HEAD moved to %d after failure at %q", head.Gen, failAt)
			}
			rep, err := st.Verify(true)
			if err != nil {
				t.Fatalf("verify after failure: %v", err)
			}
			if !rep.OK() {
				t.Fatalf("store inconsistent after failure at %q: %+v", failAt, rep)
			}
		})
	}

	// A retry after the injected failures must succeed and produce gen 2.
	res, err := st.Commit(m2, CommitOptions{Machine: "m"})
	if err != nil {
		t.Fatalf("retry commit: %v", err)
	}
	if !res.Changed || res.Gen != 2 {
		t.Fatalf("retry = %+v, want gen 2", res)
	}
}

func TestManifestDeterministic(t *testing.T) {
	mk := func(order []string) *Manifest {
		m := &Manifest{}
		for _, p := range order {
			m.Entries = append(m.Entries, Entry{
				Path: p, Type: TypeFile, Size: 1, Mode: 0o644,
				Hash: "0000000000000000000000000000000000000000000000000000000000000000",
			})
		}
		if err := m.Normalize(); err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, err := EncodeManifest(mk([]string{"a", "b", "c"}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeManifest(mk([]string{"c", "a", "b"}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("encoding depends on entry order; no-op detection would break")
	}
}

func TestDecodeRejectsBadEntries(t *testing.T) {
	bad := &Manifest{Entries: []Entry{{Path: "../x", Type: TypeDir}}}
	data, err := EncodeManifest(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(data); err == nil {
		t.Fatal("decoded manifest with unsafe path")
	}
}

func TestOpenRejectsNewerFormat(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, false); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"format":99,"repo_id":"00000000000000000000000000000000"}`)
	if err := os.WriteFile(dir+"/"+MarkerName, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("opened a store with a newer format")
	}
}
