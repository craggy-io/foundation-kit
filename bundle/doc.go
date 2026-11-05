// Package bundle loads definitions and assets from self-describing directories, and
// combines them with built-in definitions from Go code into one catalog.
//
// A bundle is a directory with a bundle.yaml manifest at its root. The manifest
// names the bundle and lists its contributions, each with an ID, a kind and a path.
// A folder without a bundle.yaml is not a bundle, and a manifest is never generated
// from a folder layout or file names, not even by Live.Install. Live.Edit writes one
// only from the contributions its caller names, one by one.
// Load owns everything that is the same for every kind (reading, path safety,
// identity, digests, linking and error reporting). A Compiler owns what a kind's
// files mean. Kinds themselves are declared in package kind.
//
// Terms in this package are defined in
// docs/decisions/ADR-0001-standard-vocabulary.md, and Live, BundleOverride, global
// kinds and dependency files are decided in ADR-0002.
//
// # Mental model
//
//   - A Source supplies the files for exactly one bundle.
//   - A Compiler turns one Contribution into a Definition of a kind.
//   - A kind.Provider supplies Definitions from Go code, with no compiler.
//   - Load turns both into an immutable Catalog, or reports every problem.
//   - All and Get read typed Definitions back out of the Catalog, and Lookup finds a
//     definition of a global kind (kind.Global) by contribution ID alone.
//   - An Override replaces one definition, and a BundleOverride layers a whole bundle
//     or provider over another, expanding into one Override per shared ID.
//   - A Compiler reads its own bundle through Contribution.Files, and the bundles it
//     declares as dependencies through Contribution.Dependency.
//   - Live holds a host's current Catalog. Reload, Install and Uninstall each run
//     Load again and swap the whole Catalog only if it succeeds. Edit changes named
//     contributions of an installed bundle and installs it. Children lists a folder
//     of installed bundles for the options function to return.
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
//     a replacement only happens through an explicit Override or BundleOverride.
//   - Effective definitions of a global kind have catalog-wide unique contribution
//     IDs. An override target isn't effective, and All lists each effective
//     definition once, under its own Ref.
//   - Live never publishes a catalog that failed to load: it keeps the last good one.
//   - Identity comes from the manifest, never from a folder name.
//
// # Concurrency
//
// A Catalog is safe for concurrent reads, and so is an Asset, because each read opens
// its Source separately. Load runs compilers concurrently, so a Compiler must not
// share mutable state without its own synchronization. A Compiler that panics is
// reported as a compile failure rather than crashing the host.
//
// Live is safe for concurrent use:
//
//   - Live.Catalog never blocks. It returns an immutable Catalog, which later changes
//     replace rather than modify, so a task should read everything from one Catalog.
//   - Reload, Install, Uninstall and Edit are serialized: one runs at a time, and
//     each calls the options function and then any subscribers while holding that
//     turn. The options function and subscribers must therefore not call any of
//     them, which would deadlock, but may call Catalog and Subscribe.
//   - Subscribers are called synchronously, in subscription order, before the change
//     returns, so start long work on another goroutine.
//   - Live coordinates changes within one process. It doesn't lock install folders
//     against other processes.
//
// # Exported names
//
//	Load, Options               run the pipeline
//	Override, BundleOverride    deliberate replacements of definitions or owners
//	Source, Dir, Embedded       where a bundle's files come from
//	Children                    list a folder of bundles as Sources
//	Compile, AnyCompiler        pair a kind with its Compiler
//	Compiler, Compiled          implement reading a kind from files
//	Contribution                what a Compiler receives, with Dependency
//	Catalog, Definition         the accepted result
//	All, Get, Lookup            read typed definitions from a Catalog
//	Live, NewLive               hold, reload, install and uninstall a live Catalog
//	Put                         a contribution for Live.Edit to add or replace
//	Asset, ErrAssetChanged      read a definition's assets on demand
//	Ref, ParseRef, Identity     how definitions and their owners are named
//	Diagnostic, LoadError       how problems are reported
//	Severity, Code              the fields callers can match on
//
// See the README for the bundle.yaml format and implementation status.
package bundle
