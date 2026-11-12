# ADR-0004: One shared taxonomy of types, in the kit

**Status:** Accepted
**Date:** 2026-11-12
**Deciders:** philcantcode

## Context

Several libraries name types of content and data. `classification-services` has a graph of 187 file-format types with parents and an `IsA` check. `capability-services` lets a capability say which types it accepts and emits, but treats the names as opaque. `script-services` describes data shapes. A host that wants to say "this capability works on any source tree, and a Java source tree is one" has nowhere to say it, because the only graph belongs to the file classifier and describes only file contents.

`AGENTS.md` said content types belong in `classification-services`, and `kind` says it is not a general type system. Both were right while one library used the graph. Craggy Harness needs extensions installed at run time to add types that other extensions and capabilities then build on, and that can't depend on the file classifier.

## Decision

Add a contract package, `taxonomy`, that holds type names and their parent relationships and nothing else.

- **`Taxonomy`** is the immutable, validated graph. It moves here from `classification-services/catalog` with its rules unchanged: lower-case dotted names, no cycles, no undeclared parents, and a type descends from its namespace when that namespace is a declared type.
- **Roots are open.** Any type with no parents is a root, so `tree` can sit beside `bytes`. The classifier keeps its own rule that its types descend from `bytes`.
- **`Live`** is a host's current taxonomy. Each owner declares a set and can replace or withdraw it at run time. A change validates the whole graph and publishes it only if it is valid, as `bundle.Live` does for catalogs.
- **Nothing else moves.** How a value is recognised as a type, and facts such as a MIME type or file extensions, stay with the library that knows them, keyed by type name. Evidence levels stay in `classification-services`.
- **No global instance.** The host creates the one `Live` and passes it in, in line with the rule against package-level state.

`classification-services` builds its catalog on `taxonomy`, and can join a host's `Live` so that its types and other owners' types form one graph.

## Options considered

### Option A: Keep the graph in `classification-services`

**Pros:** No change.
**Cons:** Every library that needs `IsA` would import a file classifier, with its Starlark runtime and parsers. Types that aren't file contents have no natural home there.

### Option B: A new library for types

**Pros:** Keeps the kit small.
**Cons:** About 300 lines with no dependencies doesn't justify a repository, and every other library would need a new dependency to name a type.

### Option C: A contract package in the kit (chosen)

**Pros:** Standard library only, so depending on it costs nothing. `capability-services`, `script-services` and hosts already import the kit.
**Cons:** The kit now owns a concept it used to send elsewhere. `AGENTS.md` and `kind` are updated to say so.

## Consequences

- **Easier:** any library can declare types and ask `IsA` without importing the classifier, and a host has one place to list every type.
- **Harder:** a type name is now shared across libraries, so two owners that pick the same name must agree on its definition. `Live` refuses a conflicting declaration.
- **Not decided here:** who may declare types in which namespace, a bundle kind for declaring types, and whether capability names belong in the same graph. Capability names stay in the registry for now.

## Action items

1. [x] Add `taxonomy` and list it as a contract.
2. [x] Build `classification-services/catalog` on it.
3. [ ] Have a host validate capability `Accepts` and `Emits` types against the live taxonomy.
4. [ ] Decide namespace ownership.
