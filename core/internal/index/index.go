// Package index caches file content hashes keyed by path and stat signature.
//
// The index is a pure cache: losing or corrupting it only costs a rehash, so
// it lives in the user state directory and never on the drive.
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Record is one cached file.
type Record struct {
	Path    string
	Dev     uint64
	Ino     uint64
	Size    int64
	MtimeNS int64
	CtimeNS int64
	Mode    uint32
	Hash    string
}

const schema = `
CREATE TABLE IF NOT EXISTS files (
	path      TEXT PRIMARY KEY,
	dev       INTEGER NOT NULL,
	ino       INTEGER NOT NULL,
	size      INTEGER NOT NULL,
	mtime_ns  INTEGER NOT NULL,
	ctime_ns  INTEGER NOT NULL,
	mode      INTEGER NOT NULL,
	hash      TEXT NOT NULL
) WITHOUT ROWID;`

// DB is an opened hash cache.
type DB struct {
	db   *sql.DB
	path string
}

// Open opens (and creates if needed) the index at path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps WAL bookkeeping and writes simple.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init index: %w", err)
	}
	return &DB{db: db, path: path}, nil
}

// Path returns the index file path.
func (d *DB) Path() string { return d.path }

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// LoadAll reads every record into a map keyed by path.
func (d *DB) LoadAll() (map[string]Record, error) {
	rows, err := d.db.Query(`SELECT path, dev, ino, size, mtime_ns, ctime_ns, mode, hash FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]Record)
	for rows.Next() {
		var r Record
		var dev, ino int64
		if err := rows.Scan(&r.Path, &dev, &ino, &r.Size, &r.MtimeNS, &r.CtimeNS, &r.Mode, &r.Hash); err != nil {
			return nil, err
		}
		r.Dev = uint64(dev)
		r.Ino = uint64(ino)
		out[r.Path] = r
	}
	return out, rows.Err()
}

// Apply upserts changed records and drops rows whose path is absent from seen.
// A nil seen map disables pruning.
func (d *DB) Apply(updates []Record, seen map[string]struct{}) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if len(updates) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO files (path, dev, ino, size, mtime_ns, ctime_ns, mode, hash)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				dev=excluded.dev, ino=excluded.ino, size=excluded.size,
				mtime_ns=excluded.mtime_ns, ctime_ns=excluded.ctime_ns,
				mode=excluded.mode, hash=excluded.hash`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range updates {
			if _, err := stmt.Exec(r.Path, int64(r.Dev), int64(r.Ino), r.Size,
				r.MtimeNS, r.CtimeNS, r.Mode, r.Hash); err != nil {
				return err
			}
		}
	}

	if seen != nil {
		var stale []string
		rows, err := tx.Query(`SELECT path FROM files`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			if _, ok := seen[p]; !ok {
				stale = append(stale, p)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(stale) > 0 {
			stmt, err := tx.Prepare(`DELETE FROM files WHERE path = ?`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, p := range stale {
				if _, err := stmt.Exec(p); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// Count returns the number of cached records.
func (d *DB) Count() (int, error) {
	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n)
	return n, err
}

// Reset drops every record. It is used when the cache is known to be stale.
func (d *DB) Reset() error {
	_, err := d.db.Exec(`DELETE FROM files`)
	return err
}
