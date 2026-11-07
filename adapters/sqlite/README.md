# adapters/sqlite

`adapters/sqlite` opens SQLite databases with settings that are safe for a long-running service, and migrates their schema by version.

```go
import "github.com/craggy-io/foundation-kit/adapters/sqlite"
```

It's a separate Go module, so only programs that import it depend on the driver, [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), which needs no C compiler. Callers see only `database/sql`.

## Opening

```go
db, err := sqlite.Open(ctx, "var/app.db", sqlite.Options{ForeignKeys: true})

// A pool of readers beside the single writer.
readers, err := sqlite.Open(ctx, "var/app.db", sqlite.Options{ReadOnly: true})
```

Every connection gets:

| Setting | Default | Option |
| --- | --- | --- |
| `busy_timeout` | 5s | `BusyTimeout` |
| `journal_mode` | WAL, and `Open` fails if the filesystem refuses it | none, read-only skips it |
| `synchronous` | FULL, so no commit is lost to power failure | `Synchronous: sqlite.SyncNormal` |
| `foreign_keys` | off | `ForeignKeys` |
| transactions | `BEGIN IMMEDIATE` on writable databases | none |
| connections | 1 writable, 4 read-only | `MaxOpenConns` |

`Pragmas` adds anything else, such as `"cache_size(-20000)"`. The path is escaped into a `file:` URI, so paths containing `?`, `#` or `%` work. `":memory:"` opens a private in-memory database.

## Migrating

```go
var schema = []sqlite.Migration{
	sqlite.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`),
	sqlite.Exec(`ALTER TABLE notes ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`),
	func(ctx context.Context, tx *sql.Tx) error { /* backfill in Go */ return nil },
}

err := sqlite.Migrate(ctx, db, schema)
```

`steps[i]` upgrades version `i` to `i+1`, and the version is kept in SQLite's `user_version`. Each step runs in its own transaction with its version change. Several processes can migrate one file at once, and each step still runs exactly once. A database newer than the program fails with `ErrNewerSchema` and isn't touched. Never edit a released step; append a new one.

## Implementation status

- Working: `Open`, `DSN`, `Migrate`, `Exec`, `Version`.
- Windows paths with a drive letter are mapped to `file:/C:/...` but haven't been tested on Windows yet.
