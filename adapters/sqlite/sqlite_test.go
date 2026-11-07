package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/craggy-io/foundation-kit/adapters/sqlite"
)

var ctx = context.Background()

func open(t *testing.T, path string, opts sqlite.Options) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(ctx, path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func pragma(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var v string
	if err := db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenEscapesAwkwardPaths(t *testing.T) {
	// Each of these means something in a SQLite URI. Windows forbids "?" in file
	// names, so use "&", which also separates URI query parameters.
	q := "?"
	if runtime.GOOS == "windows" {
		q = "&"
	}
	dir := filepath.Join(t.TempDir(), "odd "+q+"#% dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "data"+q+".db")
	db := open(t, path, sqlite.Options{})
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not created at the path given: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "data"+q+".db") {
			t.Fatalf("stray file %q: path was misparsed", e.Name())
		}
	}
}

func TestOpenSettings(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "s.db"), sqlite.Options{ForeignKeys: true, Synchronous: sqlite.SyncNormal})
	for name, want := range map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"foreign_keys": "1",
		"synchronous":  "1", // NORMAL
	} {
		if got := pragma(t, db, name); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	if db.Stats().MaxOpenConnections != 1 {
		t.Errorf("writable MaxOpenConnections = %d, want 1", db.Stats().MaxOpenConnections)
	}
	def := open(t, filepath.Join(t.TempDir(), "d.db"), sqlite.Options{})
	if got := pragma(t, def, "synchronous"); got != "2" {
		t.Errorf("default synchronous = %s, want 2 (FULL)", got)
	}
}

func TestReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	if _, err := sqlite.Open(ctx, path, sqlite.Options{ReadOnly: true}); err == nil {
		t.Fatal("read-only open created a missing database")
	}
	w := open(t, path, sqlite.Options{})
	if _, err := w.ExecContext(ctx, "CREATE TABLE t (x); INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	r := open(t, path, sqlite.Options{ReadOnly: true})
	var n int
	if err := r.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil || n != 1 {
		t.Fatalf("read = %d, %v", n, err)
	}
	if _, err := r.ExecContext(ctx, "INSERT INTO t VALUES (2)"); err == nil {
		t.Fatal("read-only database accepted a write")
	}
	if r.Stats().MaxOpenConnections != 4 {
		t.Errorf("read-only MaxOpenConnections = %d, want 4", r.Stats().MaxOpenConnections)
	}
}

func TestMemory(t *testing.T) {
	db := open(t, ":memory:", sqlite.Options{})
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal("table vanished between statements:", err)
	}
	if _, err := os.Stat(":memory:"); err == nil {
		t.Fatal("created a file named :memory:")
	}
}

var schema = []sqlite.Migration{
	sqlite.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"),
	sqlite.Exec("ALTER TABLE items ADD COLUMN size INTEGER NOT NULL DEFAULT 0; CREATE INDEX items_name ON items(name)"),
}

func TestMigrate(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "m.db"), sqlite.Options{})
	if err := sqlite.Migrate(ctx, db, schema[:1]); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, db, schema); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, db, schema); err != nil {
		t.Fatal("rerun:", err)
	}
	if v, err := sqlite.Version(ctx, db); err != nil || v != 2 {
		t.Fatalf("Version = %d, %v", v, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO items (name, size) VALUES ('a', 1)"); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, db, schema[:1]); !errors.Is(err, sqlite.ErrNewerSchema) {
		t.Fatalf("older program err = %v, want ErrNewerSchema", err)
	}
}

func TestMigrateFailedStepRollsBack(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "f.db"), sqlite.Options{})
	bad := append(schema[:1:1], sqlite.Exec("CREATE TABLE half (x); SELECT nonsense FROM nowhere"))
	if err := sqlite.Migrate(ctx, db, bad); err == nil {
		t.Fatal("bad step succeeded")
	}
	if v, _ := sqlite.Version(ctx, db); v != 1 {
		t.Fatalf("Version = %d, want 1", v)
	}
	var n int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'half'").Scan(&n)
	if n != 0 {
		t.Fatal("failed step left a table behind")
	}
}

func TestMigrateConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	var runs sync.Map
	steps := []sqlite.Migration{func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE once (x)")
		runs.Store(tx, true)
		return err
	}}
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		db := open(t, path, sqlite.Options{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = sqlite.Migrate(ctx, db, steps)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migrator %d: %v", i, err)
		}
	}
	count := 0
	runs.Range(func(any, any) bool { count++; return true })
	if count != 1 {
		t.Fatalf("step ran %d times, want 1", count)
	}
}

func TestDSN(t *testing.T) {
	got := sqlite.DSN("/tmp/a%b#c?d.db", sqlite.Options{})
	if !strings.HasPrefix(got, "file:/tmp/a%25b%23c%3Fd.db?") {
		t.Fatalf("DSN = %s", got)
	}
}
