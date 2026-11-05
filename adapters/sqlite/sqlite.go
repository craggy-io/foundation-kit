// Package sqlite opens SQLite databases with settings that are safe for a
// long-running service, and migrates their schema by version.
//
// It is a separate Go module, so that only programs that import it depend on the
// SQLite driver. The driver is modernc.org/sqlite, which needs no C compiler.
//
// Open turns a file path into a correctly escaped SQLite URI and sets, on every
// connection, a busy timeout, WAL journaling, the synchronous level and optional
// foreign keys. A writable database gets a single connection, because SQLite has
// one writer at a time. Open a second, ReadOnly database on the same path for a
// pool of readers.
//
// # Invariants
//
//   - Any file path works, including ones containing ?, # or %.
//   - A writable file database is in WAL mode when Open returns, or Open fails.
//   - Writable connections begin transactions with BEGIN IMMEDIATE, so a
//     transaction takes the write lock up front instead of failing with SQLITE_BUSY
//     halfway through.
//   - Migrate applies each schema step at most once, in its own transaction,
//     even with several processes migrating at once. A database whose schema is
//     newer than the program fails with ErrNewerSchema instead of being touched.
//
// # Concurrency
//
// The *sql.DB that Open returns is safe for concurrent use, as database/sql
// guarantees. Several processes may open one database file; WAL lets readers run
// while one writer commits.
//
// # Exported names
//
//	Open, Options              open a database
//	DSN                        the data source name Open uses
//	Synchronous, SyncFull,     how hard commits wait for the disk
//	SyncNormal
//	Migrate, Migration, Exec   versioned schema changes
//	Version                    the schema version a database is at
//	ErrNewerSchema             the database is newer than the program
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/craggy-io/foundation-kit/adapters/sqlite/internal/driver"
)

// Synchronous is SQLite's synchronous setting: how long a commit waits for the
// disk.
type Synchronous string

const (
	// SyncFull waits until a commit is on stable storage. No committed
	// transaction is lost to a power failure. This is the default.
	SyncFull Synchronous = "FULL"
	// SyncNormal is faster in WAL mode. The database can't be corrupted, but a
	// power failure can lose the most recent commits.
	SyncNormal Synchronous = "NORMAL"
)

// Options configures Open. The zero value is a writable database with durable
// commits.
type Options struct {
	// ReadOnly opens the database for reading only. The file must exist.
	ReadOnly bool
	// BusyTimeout is how long a statement waits for another connection's lock
	// before failing. Zero means 5 seconds.
	BusyTimeout time.Duration
	// Synchronous sets how commits wait for the disk. Empty means SyncFull.
	Synchronous Synchronous
	// ForeignKeys turns on foreign key enforcement.
	ForeignKeys bool
	// MaxOpenConns caps connections. Zero means 1 for a writable database and 4
	// for a read-only one. A writable database with more than one connection
	// still has only one writer at a time.
	MaxOpenConns int
	// Pragmas are further settings applied to every connection, each written as
	// the driver expects after PRAGMA, for example "cache_size(-20000)".
	Pragmas []string
}

// Open opens the SQLite database at path, creating it unless ReadOnly is set, and
// checks that it can be used. path is a file path, not a URI. The special path
// ":memory:" opens a private in-memory database with one connection.
//
// The parent directory must already exist.
func Open(ctx context.Context, path string, opts Options) (*sql.DB, error) {
	memory := path == ":memory:"
	dsn := DSN(path, opts)
	db, err := sql.Open(driver.Name, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	conns := opts.MaxOpenConns
	switch {
	case memory:
		conns = 1 // every connection would be a different database
	case conns == 0 && opts.ReadOnly:
		conns = 4
	case conns == 0:
		conns = 1
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	if err := check(ctx, db, opts.ReadOnly || memory); err != nil {
		return nil, errors.Join(fmt.Errorf("sqlite: open %s: %w", path, err), db.Close())
	}
	return db, nil
}

func check(ctx context.Context, db *sql.DB, skipWAL bool) error {
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if skipWAL {
		return nil
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal mode is %q, not WAL; the filesystem may not support it", mode)
	}
	return nil
}

// DSN returns the data source name Open uses for path, for callers that need to
// call sql.Open themselves. It is a file: URI with path percent-escaped.
func DSN(path string, opts Options) string {
	busy := opts.BusyTimeout
	if busy == 0 {
		busy = 5 * time.Second
	}
	sync := opts.Synchronous
	if sync == "" {
		sync = SyncFull
	}
	q := url.Values{}
	// busy_timeout comes first, so later pragmas that take a lock wait for it.
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(busy.Milliseconds(), 10)+")")
	if opts.ReadOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else if path != ":memory:" {
		q.Set("mode", "rwc")
		q.Add("_pragma", "journal_mode(WAL)")
	}
	q.Add("_pragma", "synchronous("+string(sync)+")")
	if opts.ForeignKeys {
		q.Add("_pragma", "foreign_keys(1)")
	}
	for _, p := range opts.Pragmas {
		q.Add("_pragma", p)
	}
	if !opts.ReadOnly {
		q.Set("_txlock", "immediate")
	}
	if path == ":memory:" {
		return "file::memory:?" + q.Encode()
	}
	return "file:" + escapePath(path) + "?" + q.Encode()
}

// escapePath turns a file path into the path of a SQLite URI. SQLite decodes %XX
// in a URI path, and ? and # end it, so those three are escaped. A Windows path
// with a volume gets a leading slash, as SQLite expects: file:/C:/dir/db.
func escapePath(path string) string {
	p := filepath.ToSlash(path)
	if vol := filepath.VolumeName(path); vol != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '%', '?', '#':
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
