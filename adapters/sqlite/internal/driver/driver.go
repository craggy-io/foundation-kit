// Package driver registers the SQLite driver, so that only this adapter's internal
// packages name it.
package driver

import _ "modernc.org/sqlite" // registers "sqlite" with database/sql

// Name is the database/sql driver name the import above registers.
const Name = "sqlite"
