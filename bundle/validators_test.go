package bundle_test

import (
	"github.com/craggy-io/foundation-kit/bundle"
	"testing"
)

func TestCatalogValidatorsRefuseActivation(t *testing.T) {
	seen := 0
	_, err := load(t, bundle.Options{Validators: []func(*bundle.Catalog) []bundle.Diagnostic{
		func(c *bundle.Catalog) []bundle.Diagnostic {
			seen++
			return []bundle.Diagnostic{{Code: "test.binding", Severity: bundle.SeverityError, Message: "missing target"}}
		},
		func(c *bundle.Catalog) []bundle.Diagnostic { seen++; return nil },
	}})
	if err == nil || seen != 2 {
		t.Fatalf("validators: seen=%d error=%v", seen, err)
	}
}
