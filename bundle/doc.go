// Package bundle loads definitions and assets from self-describing directories, and
// combines them with built-in definitions from Go code into one catalog.
//
// A bundle is a directory with a bundle.yaml manifest at its root. The manifest
// names the bundle and lists its contributions, each with an ID, a kind and a path.
// Load owns everything that is the same for every kind (reading, path safety,
// identity, digests, linking and error reporting). A Compiler owns what a kind's
// files mean. Kinds themselves are declared in package kind.
//
// Terms in this package are defined in
// docs/decisions/ADR-0001-standard-vocabulary.md.
//
// # Mental model
//
//   - A Source supplies the files for exactly one bundle.
//   - A Compiler turns one Contribution into a Definition of a kind.
//   - A kind.Provider supplies Definitions from Go code, with no compiler.
//   - Load turns both into an immutable Catalog, or reports every problem.
//   - All and Get read typed Definitions back out of the Catalog.
//   - An Asset is a file a contribution lists from assets/. A Definition carries its
//     path and digest, and its bytes are read on demand.
//
// # Invariants
//
//   - Load has no runtime side effects. Compilers do not start processes, open
//     services or call the network.
//   - Load is all-or-nothing. It never returns a partial Catalog.
//   - A Catalog and everything reachable through Contribution.Files are immutable.
//   - An Asset's bytes stay in its Source, and are never returned as verified unless
//     they match the digest accepted by Load. Reading one can therefore fail after
//     Load has succeeded, with ErrAssetChanged or fs.ErrNotExist.
//   - Duplicates are errors. Directory and Source order never choose a winner, and
//     a replacement only happens through an explicit Override.
//   - Identity comes from the manifest, never from a folder name.
//
// # Concurrency
//
// A Catalog is safe for concurrent reads, and so is an Asset, because each read opens
// its Source separately. Load may run compilers concurrently, so a Compiler must not
// share mutable state without its own synchronization.
//
// # Exported names
//
//	Load, Options, Override     run the pipeline
//	Source, Dir, Embedded       where a bundle's files come from
//	Compile, AnyCompiler        pair a kind with its Compiler
//	Compiler, Compiled          implement reading a kind from files
//	Contribution                what a Compiler receives
//	Catalog, Definition         the accepted result
//	All, Get                    read typed definitions from a Catalog
//	Asset, ErrAssetChanged      read a definition's assets on demand
//	Ref, ParseRef, Identity     how definitions and their owners are named
//	Diagnostic, LoadError       how problems are reported
//	Severity, Code              the fields callers can match on
//
// See the README for the bundle.yaml format and implementation status.
package bundle
