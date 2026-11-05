# ADR-0001: Standard vocabulary

**Status:** Proposed
**Date:** 2026-11-05
**Deciders:** philcantcode

## Context

`foundation-kit` will be read and extended by people and coding agents across several repositories. When one concept has several names, or one name covers several concepts, each reader has to work out the meaning again and new code tends to invent another word.

The research in `docs/research` proposes three different vocabularies:

| Source | Proposal | Problem for this repo |
| --- | --- | --- |
| [`argon.md`](../research/argon.md#L179), [`argon_2.md`](../research/argon_2.md#L120) | A closed set of 8 nouns and 4 verbs, and a ban on any other noun | `Unit` and `Envelope` assume the `*.unit.star` format, which the bundle design doesn't use. `Fact` and `Verdict` belong to provenance. `Module` collides with Go modules. The ban would exclude `Bundle`, `Kind` and `Catalog`. |
| [`claude.md.md`](../research/claude.md.md#L354) | 14 words built around Kit, Host, Plugin, Kind and Bundle | Most words assume a plugin host that doesn't exist here. Its `Bundle` is one instance of a kind, while this repo's bundle holds many contributions. |
| [`codex.md`](../research/codex.md#L185) | 15 nouns, each with what it must not mean, plus [lifecycle verbs](../research/codex.md#L742) | The closest fit, but it has no word for a resource type and includes product terms that this repo doesn't need yet. |

The first package, `bundle`, was stubbed before this decision and shows the cost. One contribution was called `Contribution`, `Entry` or `Item` depending on the pipeline stage, and `kind` meant two different things in `bundle.yaml`.

## Decision

Adopt the vocabulary below for exported names, package and file names, manifest fields, comments and documentation in this repository.

- Each term has one meaning, and retired terms are not used for these concepts.
- The list is open, unlike the closed set in `argon.md`. A new architectural term is added by amending this ADR in the same change that first uses it.
- Reserved terms have a fixed meaning now, so that later code doesn't reuse the word for something else.
- Ordinary Go identifiers such as local variables, and domain words inside a consumer's kind names such as `cclab.intel-pack`, are not restricted.

### Code structure

| Term | Meaning | Must not mean |
| --- | --- | --- |
| **Package** | A Go import and visibility boundary | A deployment or a product |
| **Go module** | Packages versioned together through `go.mod` | A running service, or a package |
| **Library** | Reusable code with a supported public API | A feature that imports a whole application |
| **Host** | The application that selects implementations and grants authority | A context object passed everywhere |
| **Service** | A long-lived value that owns connections, files or background work | A definition |
| **Consumer** | An application or library that imports this kit | A package inside the kit |
| **Layer** | A level of the kit that sets what a package may import. Imports point down only. | A directory |
| **Contract** | A package in the lowest layer: types and interfaces, standard library only, no I/O | A service interface owned by a consumer |
| **Engine** | A package in the middle layer: a generic mechanism that does the real work | An extension |
| **Extension** | A package in the top layer: an optional addition to an engine or contract | A plugin |
| **Test support** | A package that helps tests and is imported by no other code, named `<name>test` | Test files |
| **Adapter** | An extension that translates between the kit and one technology or peer library, in its own Go module under `adapters/` | The owner of the behaviour it translates |

### Kinds and definitions

| Term | Meaning | Must not mean |
| --- | --- | --- |
| **Kind** | A versioned type of definition, written `namespace.name@N`, bound to a Go type | The Go type alone, a content type such as `archive.zip`, or a record field type |
| **Schema** | An optional check of a definition's data, attached to a kind | The definition of the Go type |
| **Definition** | The validated, immutable value of a contribution or built-in | Its execution state |
| **Built-in** | A definition supplied by Go code rather than a bundle | A contribution |
| **Provider** | Go code that supplies built-ins, with an ID and a version | A capability provider |
| **Owner** | The bundle or provider a definition came from | Who maintains it |
| **Catalog** | The immutable set of accepted definitions and their owners' identities | A registry of live objects |
| **Ref** | A definition's qualified name, `owner-id#id` | A file path |
| **Global kind** | A kind whose effective definitions have catalog-wide unique contribution IDs, so they can be looked up by ID alone ([ADR-0002](ADR-0002-live-catalogs-and-layering.md)) | A kind registered globally |
| **Effective definition** | A definition that isn't an override target, and so is what readers see | The first definition found |
| **Identity** | An owner's ID, version, digest and origin | Only its ID |

### Bundles

| Term | Meaning | Must not mean |
| --- | --- | --- |
| **Bundle** | A versioned distribution of contributions and assets: a directory with `bundle.yaml` at its root | A running agent, container or job |
| **Manifest** | The `bundle.yaml` file at the root of a bundle | A contribution's own format |
| **Contribution** | One entry declared in a manifest, with an ID, a kind and a path | An object in a global lookup |
| **Asset** | A file under a bundle's `assets/` directory, listed by the contributions that use it. Its bytes are read on demand, by a compiler or consumer. | A contribution, or bytes held in the catalog |
| **Support file** | A file in a bundle outside `assets/` that isn't the manifest or a contribution, and is read by a compiler | An asset |
| **Compiler** | The function that turns one contribution into a definition | The loader |
| **Source** | Where the files of exactly one bundle come from | A directory of many bundles |
| **Snapshot** | The immutable copy of a bundle's accepted files, apart from its assets, which it records by size and digest | A live view of a directory |
| **Digest** | A hash of exact content | A version |
| **Dependency** | A bundle that another bundle's manifest declares it may reference | A Go dependency |
| **Override** | A deliberate replacement of one ref with another, chosen by the host | A precedence rule based on order |
| **Bundle override** | A deliberate replacement of one owner by another, which expands into one override per shared contribution ID ([ADR-0002](ADR-0002-live-catalogs-and-layering.md)) | A search path |
| **Live catalog** | A `bundle.Live`: the current catalog of a running host, replaced whole by each successful reload | A catalog changed in place |
| **Diagnostic** | One reported problem, with a stable code | A log line |

### Settings

| Term | Meaning | Must not mean |
| --- | --- | --- |
| **Setting** | A live value a person can change at run time, declared by the code that reads it | A definition, which is authored and shipped, or a `Config` field fixed at startup |

### Verbs

| Verb | Meaning |
| --- | --- |
| **New** | Construct a value, with no hidden background work |
| **Open** | Acquire something that must be released, returning a way to close it |
| **Load** | Run the bundle pipeline and return a catalog, with no runtime side effects |
| **Compile** | Turn one contribution into a definition |
| **Link** | Resolve dependencies, refs and overrides across a candidate catalog |
| **Accept** | Record digests and publish an immutable catalog |
| **Reload** | Run `Load` again for a live catalog, and swap it in only if it succeeds |
| **Install** | Place a bundle, with its own manifest, in an install folder through a validated reload |
| **Uninstall** | Remove an installed bundle through a validated reload |
| **Run** | Own a blocking loop until cancellation or failure |
| **Close** | Release what `Open` acquired |

### Reserved terms

| Term | Meaning | Must not mean |
| --- | --- | --- |
| **Capability** | A callable operation exposed at a boundary | A package, or every method |
| **Registry** | Callable capability contracts and their dispatch | A container for arbitrary objects, or the catalog |
| **Instance** | Mutable state created from a definition | The definition |
| **Run** (noun) | One execution with pinned inputs and identity | A mutable alias such as "latest" |
| **Artifact** | A stored, identifiable object with provenance | Every configuration record |

### Retired terms

| Don't use | Use instead |
| --- | --- |
| Module, on its own | Go module or package |
| Resource, for bundle content | Contribution, asset, support file or definition |
| Resource type | Kind |
| Compiled resource | Built-in |
| Plugin, in APIs | The precise term, such as bundle, kind or capability |
| Unit, Envelope, Handle | Contribution or definition, manifest, `Definition[T]` |
| Codec, Handler | Compiler |
| Pack, as an architectural term | Bundle, or a contribution of a kind that has "pack" in its name |
| Root, for a stack of bundle sources | Source |
| Entry, Item, Problem | Contribution, definition, diagnostic |

## Options considered

### Option A: Closed list from `argon.md`

| Dimension | Assessment |
| --- | --- |
| Complexity | Low: 12 words |
| Fit with this repo | Poor: it assumes a format and domain this repo doesn't have |
| Change over time | Hard: a closed list blocks legitimate new concepts |

**Pros:** It's very small, and easy to check mechanically.
**Cons:** It excludes the terms the first package needs, and treats a domain-specific list as universal.

### Option B: Vocabulary from `claude.md.md`

| Dimension | Assessment |
| --- | --- |
| Complexity | Medium: 14 words |
| Fit with this repo | Partial: many words assume a plugin host |
| Change over time | Moderate |

**Pros:** It says where each term lives, and it has `Kind`.
**Cons:** It adopts plugin architecture as a side effect of choosing words, and its `Bundle` conflicts with the contribution model.

### Option C: `codex.md` as the base, amended here and kept open (chosen)

| Dimension | Assessment |
| --- | --- |
| Complexity | Medium: more terms, but each one is already in use or reserved |
| Fit with this repo | Good: it matches the bundle design |
| Change over time | Easy: terms are added by amendment |

**Pros:** It records what each term must not mean, separates package, Go module and library, and doesn't commit the repo to a host framework.
**Cons:** An open list can grow, and needs review to stay tight.

### Option D: No ADR, rely on review

| Dimension | Assessment |
| --- | --- |
| Complexity | None up front |
| Fit with this repo | Poor: `bundle` already drifted within one stub |
| Change over time | Terms keep diverging, and renames get more expensive |

**Pros:** There's no process to follow.
**Cons:** Nothing tells a new contributor or agent which word is correct.

## Trade-off analysis

The main choice is between a closed list and an open one. A closed list is simpler to enforce, but it pushes people to reuse an ill-fitting word when something new appears, which is the problem this ADR is meant to solve. An open list with an amendment rule keeps new words deliberate, at the cost of relying on review.

Terms are adopted only when code uses them or a collision needs preventing. This leaves out words like `Feature` and `Adapter` until this repo has them.

## Consequences

- **Easier:** names in code, manifests and docs mean the same thing, and agents can be pointed at one list.
- **Harder:** a new architectural term now needs an ADR amendment, and renames must update this file.
- **Enforcement:** this relies on review for now. There's no automated check, because a banned-word test would flag legitimate uses such as "Go module".
- **Compatibility:** renaming is cheap now because nothing depends on these APIs. After a `v1` tag it would be a breaking change.

## Action items

1. [x] Apply this vocabulary to the `bundle` package.
2. [ ] Rename or rethink the planned `registry` package, because this ADR reserves that word for capabilities.
3. [ ] Decide whether the planned `lifecycle.Service` keeps `Start` and `Stop` or moves to `Run` and `Close`.
4. [x] Point `AGENTS.md` at this ADR.
