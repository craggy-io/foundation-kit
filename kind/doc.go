// Package kind declares versioned types of definitions, independently of how those
// definitions are loaded.
//
// A Kind binds a name, a version and a Go type, and can optionally carry a Schema.
// Each library declares its own kinds next to its own types, and nothing is
// registered globally. This package imports only the standard library, so declaring
// a kind doesn't pull in the bundle loader or its dependencies.
//
// A definition of a kind can come from either of two places, and both end up in the
// same catalog:
//
//   - A bundle: see package bundle, which pairs a Kind with a Compiler.
//   - Go code: a Provider supplies built-ins, which need no compiler.
//
// A kind describes a type of definition. It is not a general type system: content
// types such as "archive.zip" are named in package taxonomy, and record field
// types belong to the libraries that own them.
//
// Terms in this package are defined in
// docs/decisions/ADR-0001-standard-vocabulary.md.
//
// # Exported names
//
//	ID, ParseID             a kind's name and version, written "namespace.name@N"
//	Kind, New, AnyKind      declare a kind
//	Option, WithSchema      configure a kind
//	Global                  make a kind's contribution IDs catalog-wide
//	Schema                  optional validation of a definition's value
//	Provider                Go code that supplies built-ins
//	Builtin, AnyBuiltin     a definition supplied by Go code
package kind
