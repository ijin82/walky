package scan

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"walky/internal/index"
)

// BenchmarkWalk200k exercises the scan on a tree shaped like the target
// workload: many small files in many directories. Run it explicitly:
//
//	TMPDIR=/path/on/disk go test ./internal/scan -run '^$' \
//	    -bench BenchmarkWalk200k -benchtime 1x
func BenchmarkWalk200k(b *testing.B) {
	if testing.Short() {
		b.Skip("200k file benchmark skipped in -short mode")
	}
	const (
		files    = 200_000
		dirs     = 2_000
		perDir   = files / dirs
		fileBody = "package example\n\n// small file used by the walky benchmark\n"
	)

	root := b.TempDir()
	for d := 0; d < dirs; d++ {
		dir := filepath.Join(root, "pkg"+strconv.Itoa(d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < perDir; f++ {
			name := filepath.Join(dir, "file"+strconv.Itoa(f)+".go")
			if err := os.WriteFile(name, []byte(fileBody), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}

	db, err := index.Open(filepath.Join(b.TempDir(), "index.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := Walk(root, Options{Index: db, ForceRescan: true}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		if _, err := Walk(root, Options{Index: db}); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := Walk(root, Options{Index: db}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
