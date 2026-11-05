package bundle

import (
	"context"

	"github.com/craggy-io/foundation-kit/kind"
)

// Options selects what Load reads and how it interprets it.
//
// Everything is listed explicitly. Load does not scan for bundles or find kinds
// through init-time registration.
type Options struct {
	// Sources lists the selected bundles. Each Source supplies exactly one bundle.
	// Their order has no effect on the result.
	Sources []Source

	// Compilers lists how to read each kind from bundles. A contribution whose kind
	// has no compiler here is an error (CodeKindUnknown), not a silent skip.
	Compilers []AnyCompiler

	// Providers lists Go code that supplies built-in definitions. They need no
	// compiler, and join the same catalog as definitions from bundles.
	Providers []kind.Provider

	// Overrides lists deliberate replacements. Without one, two definitions that
	// claim the same Ref are an error.
	Overrides []Override
}

// Override makes references to Target resolve to Replacement.
//
// Both must exist in the catalog and have the same kind. Either can be a definition
// from a bundle or a built-in, so a bundle can replace a built-in default. Catalog.Explain
// reports the replacement, so the effective origin of a definition is always visible.
// The result has the replacement's assets, not the target's.
type Override struct {
	Target      Ref
	Replacement Ref
}

// Load reads every Source and Provider, and returns a complete Catalog. If anything
// is wrong it returns a *LoadError listing every problem it could find, and a nil
// Catalog.
//
// The pipeline runs in this fixed order:
//
//  1. Locate: open each Source (Source.Open).
//  2. Read: snapshot the files and parse bundle.yaml (internal/snapshot,
//     internal/manifest). Later stages only see the snapshot, so the bytes that
//     are digested are the bytes that are compiled. Assets are hashed as a stream,
//     and only their size and digest are kept.
//  3. Validate: check the manifest, paths, IDs and duplicates. Check every kind with
//     ID().Validate (CodeKindInvalid). Parse each contribution's kind with
//     kind.ParseID (CodeKindInvalid), then check that it has a compiler
//     (CodeKindUnknown). Check that each listed asset exists (CodeAssetMissing) and
//     that each file under assets/ is listed by a contribution
//     (CodeAssetUnreferenced).
//  4. Compile: call each contribution's Compiler.
//  5. Check: call Kind.Validate on every compiled value and built-in, so that schemas
//     apply however a definition was supplied (CodeSchemaInvalid).
//  6. Link: combine definitions from bundles and providers, then resolve declared
//     dependencies, Compiled.Refs and Overrides (internal/link).
//  7. Accept: compute digests (internal/digest) and publish the immutable Catalog.
//
// Each stage collects problems instead of stopping at the first. A stage only
// runs on input that passed the stage before it. Diagnostics are sorted so that
// the report is deterministic.
//
// Starting behaviour is deliberately not a stage. Owning services do that later,
// using definitions they read from the Catalog.
//
// Problems in one bundle don't stop the others being read, validated and compiled,
// so a single Load reports as much as it can. Linking needs the whole graph, so it
// only runs once every earlier stage has passed everywhere. If ctx is cancelled,
// Load returns ctx's error instead of a *LoadError.
func Load(ctx context.Context, o Options) (*Catalog, error) {
	l := &loader{kinds: map[kind.ID]kind.AnyKind{}, compilers: map[kind.ID]AnyCompiler{}}
	l.checkCompilers(o.Compilers)

	var bundles []*loadedBundle
	for _, src := range o.Sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if b := l.read(ctx, src); b != nil {
			bundles = append(bundles, b)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	providers := l.checkProviders(o.Providers)

	l.compileAll(ctx, bundles)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.failed() {
		return nil, l.loadError()
	}

	bindings := l.link(bundles, providers, o.Overrides)
	if l.failed() {
		return nil, l.loadError()
	}
	return accept(bundles, providers, o.Overrides, bindings), nil
}
