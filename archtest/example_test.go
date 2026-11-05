package archtest_test

import (
	"testing"

	"github.com/craggy-io/foundation-kit/archtest"
)

// A consumer puts a test like this anywhere in its Go module. This example only
// compiles, because it needs a real test to fail. See kit_test.go for this kit's own
// rules, which do run.
func ExampleCheck() {
	var t *testing.T // in a real test, the test's own t

	archtest.Check(t, archtest.Config{
		Packages: []string{"./cmd/...", "./features/...", "./internal/..."},
		Rules: []archtest.Rule{
			{
				Name:   "features stay below the composition root",
				Why:    "a feature receives what it needs as dependencies, so it never imports main",
				From:   []string{"./features/..."},
				Forbid: []string{"./cmd/..."},
			},
			{
				Name:   "only the data feature uses SQL",
				Why:    "other features call the data feature's API",
				From:   []string{"./features/..."},
				Except: []string{"./features/data"},
				Forbid: []string{"database/sql"},
			},
		},
	})
}
