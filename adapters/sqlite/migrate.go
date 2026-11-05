package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNewerSchema reports a database whose schema version is beyond the migrations
// the program knows, usually because a newer release has already opened it.
var ErrNewerSchema = errors.New("sqlite: database schema is newer than this program")

// Migration upgrades a schema by one version, inside the transaction Migrate gives
// it. It must not commit or roll back the transaction.
type Migration func(ctx context.Context, tx *sql.Tx) error

// Exec returns a Migration that runs a SQL script, which may hold several
// statements.
func Exec(script string) Migration {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, script)
		return err
	}
}

// Version returns the database's schema version, which Migrate keeps in SQLite's
// user_version. A new database is at 0.
func Version(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// Migrate brings db's schema to version len(steps). steps[i] upgrades version i to
// i+1, so a released step must never change: add a new one instead.
//
// Each step runs in its own transaction, together with the version change, so a
// failed step leaves the database at the previous version. The version is checked
// again inside each transaction, so several processes may migrate one database at
// once. Open the database writable with Open, whose connections begin transactions
// with BEGIN IMMEDIATE; otherwise that check can race.
func Migrate(ctx context.Context, db *sql.DB, steps []Migration) error {
	for {
		done, err := migrateOne(ctx, db, steps)
		if err != nil || done {
			return err
		}
	}
}

func migrateOne(ctx context.Context, db *sql.DB, steps []Migration) (done bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("sqlite: migrate: %w", err)
	}
	defer func() {
		if err != nil || done {
			_ = tx.Rollback()
		}
	}()
	var v int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return false, fmt.Errorf("sqlite: migrate: %w", err)
	}
	switch {
	case v == len(steps):
		return true, nil
	case v > len(steps) || v < 0:
		return false, fmt.Errorf("%w: database is at version %d, this program knows %d", ErrNewerSchema, v, len(steps))
	}
	if err := steps[v](ctx, tx); err != nil {
		return false, fmt.Errorf("sqlite: migrate to version %d: %w", v+1, err)
	}
	// PRAGMA takes no parameters; v+1 is an int, so formatting it is safe.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
		return false, fmt.Errorf("sqlite: migrate to version %d: %w", v+1, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("sqlite: migrate to version %d: %w", v+1, err)
	}
	return false, nil
}
