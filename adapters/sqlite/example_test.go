package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/craggy-io/foundation-kit/adapters/sqlite"
)

func Example() {
	ctx := context.Background()
	dir, _ := os.MkdirTemp("", "sqlite-example")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "app.db")

	db, err := sqlite.Open(ctx, path, sqlite.Options{ForeignKeys: true})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer db.Close()

	// Append steps; never edit a released one.
	err = sqlite.Migrate(ctx, db, []sqlite.Migration{
		sqlite.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`),
		sqlite.Exec(`ALTER TABLE notes ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	v, _ := sqlite.Version(ctx, db)
	fmt.Println("schema version", v)
	// Output: schema version 2
}
