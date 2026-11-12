// Package taxonomy is one shared graph of named types, where each type says
// which other types it is a kind of.
//
// A Type has a name and parents: "archive.jar" is a kind of "archive.zip",
// which is a kind of "archive". A type may have several parents, so the graph
// is not a tree. A Taxonomy is that graph, validated: it answers IsA in
// constant time, and lists ancestors and descendants. It says nothing about
// how a value is recognised as a type or what a type's data looks like; the
// libraries that know those things keep them, keyed by type name.
//
// A host keeps one Live for the process and passes it to every library that
// declares or reads types. Each owner declares its own set, building on types
// other owners declared, and may replace or withdraw that set at run time.
//
// # Invariants
//
//   - A name is dot-separated segments of lower-case letters, digits and
//     "-_+". The part before the first dot is its namespace.
//   - A type with no parents is a root. There may be several.
//   - When a type's namespace is itself a declared type, the type descends
//     from it, so namespace membership and IsA never disagree.
//   - The graph has no cycles and no parent that nobody declared.
//   - Two owners may declare the same type only when the declarations are
//     identical.
//
// New and Live report every violation at once, as an *Error whose
// diagnostics carry stable codes.
//
// # Concurrency
//
// A Taxonomy is immutable. A Live replaces its taxonomy whole on each
// successful change and never alters one in place, so a reader that took a
// Taxonomy keeps a consistent view. All Live methods are safe for concurrent
// use; there is no package-level state.
//
// A type here is not a kind. A kind is a versioned type of definition loaded
// from a bundle (package kind); a type names what a piece of content or data
// is. Terms are defined in docs/decisions/ADR-0001-standard-vocabulary.md, and
// the decision to hold types here is ADR-0004.
//
// # Exported names
//
//	Type, Set                  a type, and the types one owner declares
//	Taxonomy, New              the validated graph
//	Live, NewLive              a host's current taxonomy, changed at run time
//	Namespace, ValidName       name helpers
//	Error, Diagnostic, Code    validation failures, with the Code* constants
package taxonomy
