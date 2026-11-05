// Package bundletest helps libraries test their kinds, compilers and shipped bundles
// without starting an application.
package bundletest

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/kind"
)

// Load loads one bundle from fsys, such as a fstest.MapFS or an embed.FS, and fails
// the test if it does not load cleanly.
func Load(t testing.TB, fsys fs.FS, compilers ...bundle.AnyCompiler) *bundle.Catalog {
	t.Helper()
	c, err := bundle.Load(context.Background(), bundle.Options{
		Sources:   []bundle.Source{bundle.Embedded(t.Name(), fsys)},
		Compilers: compilers,
	})
	if err != nil {
		t.Fatalf("bundle did not load: %v", err)
	}
	return c
}

// CheckSample fails the test unless sample satisfies k's schema. Use a realistic
// value of the Go type to catch a schema and a Go type drifting apart. It also fails
// if k has no schema, because the check would then prove nothing.
func CheckSample[T any](t testing.TB, k *kind.Kind[T], sample T) {
	t.Helper()
	if k.Schema() == nil {
		t.Fatalf("%s has no schema to check against", k.ID())
	}
	if err := k.Validate(sample); err != nil {
		t.Fatalf("sample does not satisfy the schema: %v", err)
	}
}

// RequireCode fails the test unless err is a *bundle.LoadError containing code.
// Tests should match on codes, because diagnostic messages may change.
func RequireCode(t testing.TB, err error, code bundle.Code) {
	t.Helper()
	var loadErr *bundle.LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("got error %v, want a *bundle.LoadError with code %s", err, code)
	}
	for _, d := range loadErr.Diagnostics {
		if d.Code == code {
			return
		}
	}
	t.Fatalf("code %s not reported, got: %+v", code, loadErr.Diagnostics)
}
