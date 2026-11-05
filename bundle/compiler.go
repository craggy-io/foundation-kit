package bundle

import (
	"context"
	"io/fs"

	"github.com/craggy-io/foundation-kit/kind"
)

// Contribution is one entry declared in a manifest, as presented to its Compiler.
type Contribution struct {
	Ref    Ref
	Kind   kind.ID
	Bundle Identity

	// Path is the contribution's path from bundle.yaml, relative to Files.
	Path string

	// Files is the bundle's snapshot. A Compiler may read any contribution or support
	// file in its own bundle, but has no way to reach anything outside it. Assets
	// aren't in the snapshot, so read those through Assets.
	Files fs.FS

	// Assets lists the assets this contribution declares in bundle.yaml, in the order
	// written there. The resulting Definition carries the same list automatically, so
	// a Compiler only needs them if the contribution's meaning depends on their bytes.
	Assets []Asset
}

// Compiled is a Compiler's successful result.
type Compiled[T any] struct {
	// Value is the typed value that consumers read from the Catalog.
	Value T

	// Refs lists the other definitions this one needs. The Link stage reports any
	// that do not resolve, or that belong to an undeclared Dependency.
	Refs []Ref
}

// Compiler turns one contribution into a definition. See package compile for
// ready-made compilers.
//
// A Compiler gives a kind's files their meaning. It should report every problem it
// can find as Diagnostics rather than stopping at the first, and any Diagnostic with
// SeverityError rejects the load. It must have no side effects: compiling a
// contribution must not fetch data, start a process or contact a service.
type Compiler[T any] func(ctx context.Context, c Contribution) (Compiled[T], []Diagnostic)

// AnyCompiler is a kind paired with its Compiler, which is what Options.Compilers
// accepts. It is created by Compile, which keeps type erasure inside this package.
type AnyCompiler interface {
	// Kind returns the kind this compiler handles.
	Kind() kind.AnyKind

	compile(ctx context.Context, c Contribution) (compiledValue, []Diagnostic)
}

// compiledValue is a Compiled[T] with its type erased, for use inside Load.
type compiledValue struct {
	value any
	refs  []Ref
}

// Compile pairs a kind with the Compiler that reads it from a bundle's files. A kind
// only needs this if its definitions come from bundles.
func Compile[T any](k *kind.Kind[T], c Compiler[T]) AnyCompiler {
	return boundCompiler[T]{kind: k, compiler: c}
}

type boundCompiler[T any] struct {
	kind     *kind.Kind[T]
	compiler Compiler[T]
}

func (b boundCompiler[T]) Kind() kind.AnyKind { return b.kind }

func (b boundCompiler[T]) compile(ctx context.Context, c Contribution) (compiledValue, []Diagnostic) {
	out, diags := b.compiler(ctx, c)
	return compiledValue{value: out.Value, refs: out.Refs}, diags
}
