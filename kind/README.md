# kind

`kind` declares versioned types of definitions, separately from how those definitions are loaded. A kind binds a name, a version and a Go type, and can optionally carry a schema.

```go
import "github.com/craggy-io/foundation-kit/kind"
```

This package imports only the standard library, so a library can declare its kinds without depending on the bundle loader. Terms used here are defined in [ADR-0001](../docs/decisions/ADR-0001-standard-vocabulary.md).

## Declaring a kind

Each library declares its own kinds next to its own types. Nothing is registered globally, and there's no central list to update.

```go
var Profile = kind.New[ProfileSpec]("example.profile", 1)
```

A kind is written `namespace.name@N`, for example `cclab.intel-pack@1`. The name is lower-case dot-separated segments of letters, digits and hyphens, and it needs at least two segments so that kinds from different libraries can't collide. Versions start at `1` and have no leading zeros.

`ParseID` splits that string into an `ID` and rejects anything else. `kind.New` never fails, so that kinds can be package-level variables, and `bundle.Load` will check every kind it receives.

## Adding a schema

A schema is an optional check on top of the Go type. It suits data-shaped kinds, and a Starlark or Markdown kind would usually have none.

```go
var Profile = kind.New[ProfileSpec]("example.profile", 1, kind.WithSchema(profileSchema))
```

`Kind.Validate` checks that a value has the kind's Go type, then passes the value's JSON form to the schema. A schema and a Go type can drift apart, so test them together with `bundletest.CheckSample`.

## Global kinds

Most definitions are named by their full reference, `owner-id#id`. Some are looked up by contribution ID alone, whichever bundle defines them, such as agent or profile names. Declare those kinds global:

```go
var Agent = kind.New[AgentSpec]("example.agent", 1, kind.Global())
```

`bundle.Load` then rejects two effective definitions of the kind with the same contribution ID (`bundle.id_conflict`), and `bundle.Lookup` finds one by that ID.

## Definitions from Go code

Definitions can come from Go code as well as from bundles, and both end up in the same catalog. A `Provider` supplies built-ins, which need no compiler:

```go
var Defaults = kind.Provider{
	ID:      "example.defaults",
	Version: "1.0.0",
	Builtins: []kind.AnyBuiltin{
		kind.Builtin(Profile, "default", ProfileSpec{Retries: 3}),
	},
}
```

A built-in has no file bytes to hash, so its digest is empty and its provider's `Version` is all that identifies its content.

## What isn't a kind

A kind describes a type of definition. It isn't a general type system:

| Concept | Is it a kind? |
| --- | --- |
| A `classification-services` definition module | Yes |
| A `data-services` source declaration | Yes |
| A content type such as `archive.zip` | No, it's data inside a definition |
| A record field type such as `data.Field` | No, it's part of a source declaration |

## Implementation status

Already working and tested:

- `ID`, `ParseID` and `ID.Validate`
- `New`, `WithSchema`, `Global` and `Kind.Validate`
- `Provider` and `Builtin`

- `jsonschema.New`, backed by `github.com/santhosh-tekuri/jsonschema/v6`. It does no I/O, so a `$ref` outside the document is an error.

Nothing is waiting to be built.
