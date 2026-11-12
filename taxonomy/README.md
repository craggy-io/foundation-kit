# taxonomy

`taxonomy` is one shared graph of named types, where each type says which other types it is a kind of. A Java source tree is a kind of source tree, which is a kind of folder tree; an APK is a kind of ZIP archive and also a kind of Android file.

```go
import "github.com/craggy-io/foundation-kit/taxonomy"
```

This package imports only the standard library and does no I/O. Terms used here are defined in [ADR-0001](../docs/decisions/ADR-0001-standard-vocabulary.md), and [ADR-0004](../docs/decisions/ADR-0004-shared-taxonomy.md) records why types live in the kit.

## Declaring types

A host creates one `Live` and passes it to every library that declares or reads types. Each owner declares its own set, which may build on types that other owners declared.

```go
live := taxonomy.NewLive()

live.Declare("host", []taxonomy.Type{
    {Name: "tree", Description: "A folder and everything under it"},
    {Name: "source-tree", Parents: []string{"tree"}},
})

// Later, at run time, an extension adds its own.
live.Declare("java-extension", []taxonomy.Type{
    {Name: "source-tree.java", Parents: []string{"source-tree"}},
})
```

- **Declare** replaces everything that owner declared before, so call it again with the full set to change it.
- **Preview** returns the taxonomy `Declare` would publish, without publishing it.
- **Withdraw** removes an owner's types. It is refused while another owner's types still descend from them.

Every change validates the whole graph first. If it fails, the published taxonomy stays as it was.

## Reading types

`Live.Taxonomy()` returns the current `Taxonomy`, which is immutable. Hold on to it for as long as you need one consistent view.

```go
t := live.Taxonomy()
t.IsA("source-tree.java", "tree")   // true
t.Ancestors("source-tree.java")     // [source-tree tree]
t.Descendants("tree")               // [source-tree source-tree.java]
t.Owners("source-tree.java")        // [java-extension]
```

`Depth` is the longest path to a root, so a deeper type is more specific. `Closure` adds every ancestor to a set of types. `Revision` identifies the content of the whole taxonomy, for cache keys.

A library that has no host to share with can build a taxonomy directly with `taxonomy.New(sets...)`.

## Rules

- A name is dot-separated segments of lower-case letters, digits and `-_+`. The part before the first dot is its namespace.
- A type with no parents is a root. There can be several, such as `bytes` and `tree`.
- When a type's namespace is itself a declared type, the type must descend from it, so namespace membership and `IsA` always agree.
- Cycles and parents that nobody declared are errors.
- Two owners may declare the same type only when both declarations are identical.

A failed validation returns an `*Error` that lists every problem at once. Match on `Diagnostic.Code`, never on the message.

| Code | Meaning |
| --- | --- |
| `taxonomy.owner_invalid` | An owner is empty or listed twice |
| `taxonomy.name_invalid` | A type name is malformed |
| `taxonomy.type_conflict` | Two owners declare one type differently |
| `taxonomy.parent_unknown` | A parent isn't declared by anyone |
| `taxonomy.cycle` | A type is its own ancestor |
| `taxonomy.namespace_escape` | A type doesn't descend from its declared namespace |
| `taxonomy.owner_required` | An owner can't be withdrawn while others build on its types |

## What stays out

The taxonomy holds names and relationships only. How content is recognised as a type, what a type's data looks like, and facts such as a MIME type or file extensions stay in the library that knows them, keyed by type name. `classification-services` keeps its recognisers and file-format facts this way.

A type is not a kind. A kind (package `kind`) is a versioned type of definition loaded from a bundle. A type names what a piece of content or data is.

## Implementation status

- [x] `Type`, `Taxonomy` and `New`, with `IsA`, `Ancestors`, `Descendants`, `Depth`, `Closure`, `Roots`, `Owners` and `Revision`
- [x] `Live` with `Declare`, `Preview` and `Withdraw`, all-or-nothing
- [x] Every problem reported at once, with stable codes
- [ ] Change notification: a reader compares `Revision` to notice a change
- [ ] A bundle kind for declaring types from a bundle. Owners call `Declare` from Go for now
- [ ] Ownership of namespaces: any owner may declare a type in any namespace
