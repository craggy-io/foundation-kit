# Foundation Kit: coding-agent guide

Use this guide when editing this repository or building on its Go module, `github.com/craggy-io/foundation-kit`. Consumers should give this guide to their coding agent explicitly, because installing a dependency doesn't mean its `AGENTS.md` gets read.

Use the terms in [ADR-0001](docs/decisions/ADR-0001-standard-vocabulary.md), and don't use its retired terms. [ADR-0002](docs/decisions/ADR-0002-live-catalogs-and-layering.md) decides how catalogs are layered, reloaded and installed into. [`docs/decisions`](docs/decisions) is binding. [`docs/research`](docs/research) is background only: its proposals conflict, so never treat one as a decision.

## Current state

This kit is early, so check before relying on anything:

- The `bundle` pipeline works end to end, including `Live` reloading and installs. Consumers are being migrated to it now, so expect its API to change as real kinds are ported.
- Each package's README has an implementation status list. Update it in the same change as the code.

## Choose the owning package

| Need | Package |
| --- | --- |
| Declare a kind, a schema or a built-in | [`kind`](kind/README.md) |
| Load bundles and built-ins, read the catalog | [`bundle`](bundle/README.md) |
| Reload, install or uninstall bundles at run time | `bundle.Live` |
| Compile a single-file contribution | `bundle/compile` |
| Validate a kind against JSON Schema | `kind/jsonschema` |
| Test a kind, compiler or bundle | `bundle/bundletest` |
| Declare a live, user-editable value | [`settings`](settings/README.md) |
| Enforce import rules in a test | [`archtest`](archtest/README.md) |
| Replace or create a file atomically, or lock one | [`fsx`](fsx/README.md) |
| Hash JSON so equal values match | [`canonjson`](canonjson/README.md) |
| Honour a server's `Retry-After` | [`retryafter`](retryafter/README.md) |
| Announce changes to readers with their own cursor | [`feed`](feed/README.md) |
| Open and migrate a SQLite database | [`adapters/sqlite`](adapters/sqlite/README.md) |

## Layers

Imports point down only. A package's directory doesn't decide its layer, so `bundle/compile` is an extension even though it sits under an engine.

| Layer | Packages | May import | Must not |
| --- | --- | --- | --- |
| Extensions | `bundle/compile`, `kind/jsonschema`, `adapters/sqlite` (own module) | Engines, contracts, own dependencies | Import another extension, or be imported by an engine |
| Engines | `bundle`, `settings`, `fsx`, `canonjson`, `retryafter`, `feed` | Contracts, small dependencies hidden in `internal/` | Import another engine without an ADR, or expose a third-party type in its API |
| Contracts | `kind` | Standard library only | Do I/O, or import any other kit package |
| Test support | `bundle/bundletest`, `archtest` | Any layer, plus `testing` | Be imported by non-test code |

- **`internal/`:** belongs to its parent package, and has the same layer and limits.
- **External tests:** `package x_test` files may import higher layers, as `bundle/example_test.go` does with `compile`. Normal test files can't, because that creates an import cycle.
- **Heavy dependencies:** put an extension with a heavy or optional dependency in its own Go module under `adapters/<technology>/`, with its own `go.mod`. Root `go test ./...` doesn't reach nested modules, so add each one to the checks below.

## Does it belong here?

Add something to the kit only if all of these are true:

1. **It's generic.** It works unchanged in both an APK research tool and a game's quest pipeline, and needs no product word such as "lab" or "APK".
2. **It has a real consumer.** Something needs it now, and a second independent use is known or expected. Don't add speculative packages.
3. **No existing library owns it.** Starlark belongs in `script-services`, callable operations in `capability-services`, content types such as `archive.zip` in `classification-services`.

If not, put it in the consumer. Kinds and compilers for a concept live with the library that owns that concept, never here.

## Adding a package

1. Pick the lowest layer that can hold it, then add it to the tables in this file, the [root README](README.md) and [`archtest/kit_test.go`](archtest/kit_test.go).
2. Name it for what it provides. Don't use `util`, `common`, `helpers`, `types`, `manager` or `core`.
3. Add `doc.go` with its mental model, invariants, concurrency rules and exported names.
4. Add a `README.md` with usage and an implementation status list.
5. Add tests, plus an `example_test.go` for the main use. Add test support as `<name>test/` only once two packages need it.
6. Default to unexported or `internal/`. Promoting later is safe, while demoting breaks consumers.
7. Add any new architectural term to ADR-0001 in the same change, and write a new ADR for a decision that affects other packages.

## Rules for all packages

- **No global state:** don't use `init()` registration or package-level mutable state. Hosts list what they use. A package-level `kind.New` is fine, because it's immutable.
- **Lifecycle verbs:** `New` constructs, `Open` acquires something and returns a way to release it, `Run` blocks until cancelled, `Close` releases. Immutable values need none of these.
- **Context:** blocking and I/O functions take `ctx context.Context` first.
- **Errors:** report every problem you can find, not just the first. Give machine-readable problems a stable code, and match on the code rather than the message.
- **Type erasure:** keep `any` behind one sealed `Any*` interface, so consumers never cast.
- **Immutability:** anything published to concurrent readers is immutable. Replace it rather than changing it in place.
- **Stubs:** a stub returns an `ErrNotImplemented` or panics, has a `TODO` saying what's missing, and is listed in its README. It must never return a plausible empty value.
- **Compatibility:** nothing is stable before the first release, so remove old code rather than adding shims or aliases. After that, breaking changes need a major version.

## Building on the kit

- **Kinds:** declare each with `kind.New` next to the Go type it produces, written `namespace.name@N`. Change `N` when the meaning of the content changes.
- **Manifests:** every bundle has a real `bundle.yaml`, in shipped content and test fixtures alike. Never generate one from a folder layout or file names, and never add a loader beside `bundle.Load`.
- **Compilers:** use `bundle/compile` when a file maps straight onto a struct. A custom `Compiler` must have no side effects, must read only through `Contribution.Files`, `Contribution.Assets` and `Contribution.Dependency`, and must give the same result for the same inputs, because `Live` reuses compiled values.
- **Definitions:** read them with `bundle.All`, `bundle.Get` and, for kinds declared with `kind.Global()`, `bundle.Lookup`. Start services from those definitions after `Load` returns, never during it.
- **Reloading:** hold a running host's catalog in a `bundle.Live`, and install or remove bundles through `Live.Install` and `Live.Uninstall`, so that every change is a validated `Load`.
- **Assets:** put files that consumers read on demand under `assets/`, and list them on the contributions that use them. Handle read errors, because `Asset.Read` and `Asset.Open` can fail after `Load` has succeeded.
- **Built-ins:** bump `Provider.Version` whenever a built-in's value changes. The catalog digest can't detect the change otherwise.
- **Schemas:** test each one against its Go type with `bundletest.CheckSample`.
- **Overrides:** replace a definition only with an explicit `Override`, or a whole bundle with a `BundleOverride`. Order never picks a winner.
- **Trust:** compilers run in-process, so loading isn't a sandbox. Only load trusted content.

## Developing and verifying

- Reread files before editing, and preserve concurrent changes.
- Commit no `replace` directives. Use an uncommitted `go.work` to develop alongside a consumer.
- Keep code, READMEs and `doc.go` exported-name lists in step.
- `go test` enforces the layer rules through [`archtest/kit_test.go`](archtest/kit_test.go). It can't see inside nested Go modules, so give each one its own `archtest.Check`.
- Report what you ran and what you didn't. An unrun check is not a pass.

```sh
go build ./... && go vet ./... && go test ./...
(cd adapters/sqlite && go vet ./... && go test ./...)
gofmt -l .    # must print nothing
```
