package machineid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetCreatesAndReusesID(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)

	id1, err := Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(id1) != 32 {
		t.Fatalf("id len = %d, want 32", len(id1))
	}
	id2, err := Get()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("id changed: %q vs %q", id1, id2)
	}

	// Corrupt file is replaced with a fresh valid id.
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(xdg, "walky", "machine-id") {
		t.Fatalf("Path = %q", p)
	}
	if err := os.WriteFile(p, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id3, err := Get()
	if err != nil || len(id3) != 32 || id3 == "short" {
		t.Fatalf("regenerated id = %q, %v", id3, err)
	}
}
