# ADR-0002: Live catalogs and layering

**Status:** Proposed
**Date:** 2026-11-05
**Deciders:** philcantcode

## Context

Every craggy-io repository is moving its authored content (scripts, definitions, profiles, skills, workflows) onto `bundle.Load`. Porting the first consumers showed four needs that the first version of `bundle` didn't meet:

1. **Layering.** Hosts stack content: a project's bundle over a user's, over what ships with the application. With only `Override`, the host would have to list every contribution ID of every layer, and update the list whenever a layer changed.
2. **Names without owners.** Agent names, classifier modules, capability prefixes, data source names and profile names are looked up by contribution ID alone. Nothing stopped two bundles from defining the same name, and nothing found a definition by that name.
3. **Shared files.** A Starlark `load()` needs to read a library that lives in another bundle. A compiler could only see its own bundle.
4. **Running hosts.** Hosts reload content while running and install bundles at run time. Without a shared mechanism, each would write its own watcher and install path beside `Load`, which is exactly the duplication `bundle` exists to remove.

Two rules from earlier decisions constrain the answer. [ADR-0001](ADR-0001-standard-vocabulary.md) and the bundle rules say order never picks a winner: replacements are explicit. And the user has decided that `bundle.yaml` is always required: no code generates or synthesizes a manifest from a folder layout or file names.

## Decision

### Bundle-level overrides

`Options.BundleOverrides` lists `BundleOverride{Target, Replacement}` pairs of owner IDs (a bundle's `metadata.id` or a provider's ID). Each one expands, inside the Link stage, into one `Override` per contribution ID the two owners share. There is no second override mechanism: the expanded overrides go through exactly the checks an explicit `Override` does, so kinds must match and a target can't be overridden twice.

- An owner can be the target of at most one `BundleOverride`, so they form chains.
- A chain is flattened: each contribution resolves to the highest owner in the chain that has its ID, so every override still takes one step.
- Contributions that only the replacement has are added, and those only the target has are kept.
- Unknown owners, self-overrides, an owner targeted twice, and cycles are `bundle.override_invalid`.
- `Catalog.Overrides` and `Catalog.Explain` report every applied replacement, so consumers can report what was shadowed. Bundle overrides change the catalog digest through its bindings.

### Global kinds

`kind.Global()` marks a kind whose contribution IDs must be unique across the catalog. The Link stage reports `bundle.id_conflict` for every effective definition of a global kind that shares its contribution ID with another. An *effective* definition is one that isn't an override target, so an override is how two layers legitimately define the same name. `bundle.Lookup(c, k, id)` returns the effective definition by contribution ID, and reports false for a kind that isn't global.

`All` lists effective definitions only, each once under its own `Ref`. `Get` on an override target still returns the replacement under the target's `Ref`.

### Dependency files

`Contribution.Dependency(id)` returns the snapshot and identity of a bundle the contribution's manifest declares in `dependencies`, and nothing else: no undeclared bundle, and no provider, which has no files. The snapshot is the same immutable copy that was digested. Because a compiler may read its dependencies, their digests are part of the key used to reuse compiled values.

### Live

`bundle.Live` holds a host's current catalog.

- **One path.** `NewLive` takes a function returning `Options`, which runs on every load so the host can list install folders again with `Children`. Reloads, installs and uninstalls all call it and `Load`. Nothing reads bundles another way.
- **Whole-catalog last-good.** A change either produces a complete new catalog, or fails and leaves the previous one current. There is no partial or per-bundle reload. The new catalog is swapped in atomically, and readers holding the old one keep a consistent, immutable view.
- **Change detection.** A reload swaps only when the catalog digest or an owner's identity changed, and then calls subscribers synchronously, in order.
- **Compile reuse.** A reload passes the previous catalog to the pipeline as an internal option, not a second pipeline. A contribution whose bundle digest, origin, declared dependencies' digests and kind are unchanged reuses its compiled value, which is still checked against its kind's schema. Compilers must therefore depend only on what `Contribution` exposes.
- **Install is a validated reload.** `Install(ctx, dir, files)` requires `files` to contain a `bundle.yaml`, which is never generated. It stages the files in a hidden folder under `dir`, moves any existing `dir/<metadata.id>` aside, moves the new folder into place, and reloads. If the load fails, or its catalog doesn't list the bundle at `dir/<metadata.id>`, the previous folder is put back, the catalog is unchanged, and the `*LoadError` is returned.
- **Edit is explicit, not generated.** `Edit(ctx, dir, id, put, remove)` rewrites `dir/<id>/bundle.yaml` only from the contributions its caller names, keeping metadata, dependencies and assets, so hosts stop hand-writing manifests. It then goes through `Install`, or `Uninstall` when nothing is left, so validation and rollback stay in one place.
- **Uninstall is the reverse.** `Uninstall(ctx, dir, id)` moves `dir/<id>` aside and reloads, putting it back if the load fails. Removing a bundle that another declares as a dependency therefore fails.
- **Concurrency.** `Catalog` never blocks. Changes are serialized, and the options function and subscribers run during a change, so they must not start another one.

### Manifests are always required

Every bundle, shipped, installed or in a test fixture, has a real `bundle.yaml`. `Children` skips folders without one rather than inventing it, and leaves it to the host to decide whether that's an error. `Install` rejects files without one.

## Options considered

### Install: validate beside the options function, then move

The first design validated the staged folder as an extra `Source` added to the options function's result, and moved it into place only after that load succeeded. It was rejected during implementation: the options function can't see a candidate it didn't list, so a host rule that depends on installed content, such as a `BundleOverride` naming the installed bundle, can't apply to it, and every such install fails. Uninstall had the same problem in reverse. Moving first, then validating with rollback, lets the options function see the folder exactly as it will be, at the cost of a window in which other processes can see an unvalidated folder, and of not being crash-safe.

### Layering by source order

Rejected by ADR-0001's rule that order never picks a winner. A list order is easy to get wrong silently, and `Explain` couldn't say why a definition won.

### Global uniqueness for every kind

Rejected. Most kinds are referred to by full `Ref`, and two bundles defining `#main` is normal. Uniqueness is opt-in per kind.

### Per-bundle reload

Rejected. Linking needs the whole graph, so a reload that kept some old definitions could publish a catalog that no single `Load` would accept.

## Consequences

- **Easier:** hosts get layering, name lookup, shared libraries, hot reload and runtime installs from one pipeline, and every change reports diagnostics the same way.
- **Harder:** compilers must be deterministic in their `Contribution`, and a compiler change that alters output needs a new kind version, or reloads keep old values.
- **Cost:** each reload snapshots and hashes every bundle. Only compiling is skipped.
- **Limits:** `Install` and `Uninstall` aren't crash-safe and don't lock against other processes.

## Action items

1. [x] Implement `BundleOverride`, `kind.Global`, `Lookup`, `Contribution.Dependency`, `Children` and `Live` in `bundle`.
2. [x] Add the new terms to ADR-0001.
3. [ ] Migrate every craggy-io loader onto `Load` and `Live`, and delete the old loaders.
