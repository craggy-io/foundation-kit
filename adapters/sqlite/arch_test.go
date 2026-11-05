package sqlite_test

import (
	"testing"

	"github.com/craggy-io/foundation-kit/archtest"
)

// This adapter is its own Go module, so the kit's layer test can't see inside it.
func TestArchitecture(t *testing.T) {
	archtest.Check(t, archtest.Config{
		Packages: []string{".", "./internal/..."},
		Rules: []archtest.Rule{{
			Name:   "the adapter hides its driver",
			Why:    "callers see only database/sql, so the driver can change without breaking them",
			From:   []string{"."},
			Forbid: []string{archtest.External},
		}},
	})
}
