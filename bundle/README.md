# bundle

`bundle` loads definitions and assets, such as scripts, prompts and schemas, from directories that describe themselves with a `bundle.yaml` file, and combines them with built-in definitions from Go code into one catalog. It replaces a separate loader for each kind of content with one pipeline: you say how a kind is read, and `Load` handles path safety, identity, digests, linking and error reporting.

```go
import "github.com/craggy-io/foundation-kit/bundle"
```

> **Status: stub.** The public API compiles, but it can't load a bundle yet. `Load` and `Dir` return `ErrNotImplemented`. See [Implementation status](#implementation-status).

Kinds are declared in the [`kind`](../kind/README.md) package. Terms used here are defined in [ADR-0001](../docs/decisions/ADR-0001-standard-vocabulary.md).

## Bundle layout

A bundle is a directory with exactly one `bundle.yaml`, at its root. Assets go in an `assets/` directory at the root. Everything else is laid out however suits the author, because the manifest gives each contribution's path.

```text
android-research/
├── bundle.yaml
├── assets/
│   ├── models/
│   │   └── classifier.bin
│   └── prompts/
│       └── review.md
├── scripts/
│   └── gate-audit.star
├── agents/
│   ├── team.star
│   └── helpers.star
└── schemas/
    └── reviewer-output.json
```

## bundle.yaml

```yaml
apiVersion: craggy.io/bundle/v1

metadata:
  id: example.android-research   # required: the bundle's identity
  version: 0.1.0                 # required: the author's release label
  title: Android research        # optional

dependencies:                    # optional: bundles this one may reference
  - id: example.shared-prompts
    digest: sha256:...           # optional: pin to exact content

contributions:                   # the entries in this bundle
  - id: audit                    # required: unique within the bundle
    kind: example.script@1       # required: selects the compiler
    path: scripts/gate-audit.star  # required: relative to this file

  - id: research-team
    kind: example.agent-team@1
    path: agents/team.star
    assets:                      # optional: files this contribution uses from assets/
      - assets/prompts/review.md
      - assets/models/classifier.bin
```

A definition is referred to as `owner-id#id`, where the owner is its bundle or provider, for example `example.android-research#audit`.

## Assets and support files

A bundle has two sorts of file besides its manifest and contributions, and both count towards the bundle's digest:

| | Asset | Support file |
| --- | --- | --- |
| **Where it lives** | Under `assets/` at the bundle root | Anywhere else |
| **Declared in `bundle.yaml`** | Yes, under the contributions that use it | No |
| **Held in memory after `Load`** | No, only its path, size and digest | Yes, in the snapshot |
| **Read by** | Compilers and consumers, through `Asset` | Compilers, through `Contribution.Files` |
| **Example** | A prompt or model a service uses at runtime | A helper module a script loads at compile time |

`Load` reads each asset once to hash it, without holding its bytes. Every definition then lists its contribution's assets, and reading them is up to you:

```go
def, ok := bundle.Get(catalog, Team, ref)
if !ok {
	return fmt.Errorf("no definition %s", ref)
}

// Read a small asset whole. No bytes are returned unless they all match the digest.
prompt, ok := def.Asset("assets/prompts/review.md")
if !ok {
	return errors.New("no review prompt")
}
data, err := prompt.Read(ctx)

// Stream a large one. Its content is only verified when the stream reaches EOF.
model, ok := def.Asset("assets/models/classifier.bin")
if !ok {
	return errors.New("no classifier")
}
r, err := model.Open(ctx)
```

A contribution's assets are also in `Contribution.Assets`, for a compiler whose result depends on their bytes.

Because the bytes stay in the `Source`, reading an asset can fail after `Load` has succeeded:

- **Changed content:** the read returns `ErrAssetChanged`, because the bytes no longer match the digest in the catalog.
- **Removed file:** the read returns an error matching `fs.ErrNotExist`.

A `Source` must therefore keep working for as long as its catalog is in use. An `Embedded` source can't change, so it's the safest choice for assets that must always be readable.

## Rules

`Load` enforces these for every kind:

- **Identity comes from the manifest.** Renaming a folder changes nothing.
- **There is one manifest per bundle.** A `bundle.yaml` anywhere other than the root is an error.
- **The YAML is strict.** Duplicate keys, aliases, merge keys, custom tags and unknown fields are errors.
- **Paths stay inside the bundle.** Escaping paths and symlinks are errors.
- **Assets stay in `assets/`.** A listed asset must be a file under `assets/`, and a contribution's `path` must be outside it.
- **Assets are declared.** A listed asset that doesn't exist is an error, and so is a file under `assets/` that no contribution lists. Contributions can share an asset.
- **Malformed kinds and kinds with no compiler are errors.** To disable a feature, leave its bundles out of `Options.Sources`.
- **Schemas apply to every definition.** If a kind has a schema, it checks compiled values and built-ins alike.
- **Duplicates are errors.** Order never picks a winner, and replacing something requires an explicit `Override`.
- **References are declared.** A reference into another bundle must be listed in `dependencies`.
- **Every problem is reported at once.** Reports are deterministic, and a failed load returns no catalog.
- **Loading has no side effects.** Listing a definition must not start or contact anything.

## Usage

Declare each kind once, in the library or application that owns it, then give `Load` everything the application uses:

```go
var Profile = kind.New[ProfileSpec]("example.profile", 1)

catalog, err := bundle.Load(ctx, bundle.Options{
	// Definitions from bundles: each Source is one bundle.
	Sources: []bundle.Source{
		bundle.Embedded("defaults", defaultsFS), // root contains bundle.yaml
		bundle.Dir("res/android-research"),      // so does this directory
	},
	Compilers: []bundle.AnyCompiler{
		bundle.Compile(Profile, compile.JSON[ProfileSpec]()),
	},

	// Definitions from Go code join the same catalog.
	Providers: []kind.Provider{{
		ID:       "example.builtins",
		Version:  "1.0.0",
		Builtins: []kind.AnyBuiltin{kind.Builtin(Profile, "default", ProfileSpec{Retries: 3})},
	}},
})
if err != nil {
	return err // a *bundle.LoadError listing every problem
}

for _, def := range bundle.All(catalog, Profile) {
	fmt.Println(def.Ref, def.Value.Retries) // def.Value is a ProfileSpec
}
```

Sources are listed explicitly because `Load` doesn't scan directories for bundles.

## Ready-made compilers

Most kinds just read one file into a value, so the `compile` package covers them. A kind with extra rules can wrap one of these or write its own `Compiler`.

| Compiler | Value |
| --- | --- |
| `compile.Text()` | The file's contents as a string |
| `compile.JSON[T]()` | The file decoded into `T`, rejecting unknown fields and trailing data |
| `compile.YAML[T]()` | The file decoded into `T` under the same strict rules as `bundle.yaml` (not implemented yet) |

## Pipeline

| Stage | What happens | Code |
| --- | --- | --- |
| Locate | Open each selected source | `source.go` |
| Read | Snapshot the files, hash the assets and parse `bundle.yaml` | `internal/snapshot`, `internal/manifest` |
| Validate | Check the manifest, paths, assets, IDs, kinds and duplicates | `internal/manifest`, `load.go` |
| Compile | Call each contribution's compiler | `compiler.go` |
| Check | Apply each kind's schema to compiled values and built-ins | `kind.Kind.Validate` |
| Link | Combine bundles and providers, then resolve dependencies, references and overrides | `internal/link` |
| Accept | Compute digests and publish the immutable catalog | `internal/digest`, `catalog.go` |

Nothing is started by `Load`. Services read what they need from the catalog and start it themselves.

## Package layout

| Path | Purpose |
| --- | --- |
| `load.go` | `Load`, `Options`, `Override` |
| `compiler.go` | `Compile`, `AnyCompiler`, `Compiler[T]`, `Compiled[T]`, `Contribution` |
| `catalog.go` | `Catalog`, `Definition[T]`, `All`, `Get` |
| `asset.go` | `Asset`, `ErrAssetChanged` |
| `source.go` | `Source`, `Dir`, `Embedded` |
| `reference.go` | `Ref`, `ParseRef`, `Identity` |
| `diagnostic.go` | `Diagnostic`, `LoadError`, severities and codes |
| `compile/` | Ready-made compilers |
| `bundletest/` | Helpers for testing kinds, compilers and shipped bundles |
| `internal/manifest/` | Strict `bundle.yaml` parsing and validation |
| `internal/snapshot/` | Path policy, immutable file copies and asset hashing |
| `internal/digest/` | Canonical file, bundle and catalog hashing |
| `internal/link/` | Reference, dependency and override resolution |
| `testdata/` | Valid and deliberately invalid bundles |

## Out of scope

- **Kind-specific logic:** compilers live with whatever owns the kind, and Starlark evaluation isn't part of this package.
- **Host-owned concerns:** secrets, capability grants and enablement stay outside bundles. A bundle must not be able to authorize itself.
- **Starting and supervising:** that belongs to the owning services.
- **Distribution:** remote fetching, archives, version solving and install hooks aren't included. Fetch and verify remote content first, then present it as a `Source`.
- **Hot reload:** not in the first version. Because a catalog is immutable, a reload can later build a new one and swap it in.
- **Sandboxing:** compilers run in-process, so loading is for trusted content only.

## Known limitations

- **Built-in identity:** a built-in has no file bytes, so its digest is empty. The catalog digest covers only its provider's ID and version, so changing a built-in's value without changing that version doesn't change the digest.
- **Built-in references:** a built-in can't declare references to other definitions.
- **`compile.JSON`:** it doesn't detect duplicate keys, and its diagnostics have no line numbers.
- **Asset load time:** `Load` hashes every asset on every load, so very large assets slow startup even though their bytes aren't kept.
- **Asset streaming:** bytes from `Asset.Open` are unverified until the stream reaches EOF.
- **Asset paths:** an asset is only reachable as a stream, so a tool that needs a real file path has to copy it to disk first.
- **Old assets after a reload:** a catalog doesn't keep its own copy of its assets. If a directory is updated in place, reads through the old catalog fail with `ErrAssetChanged`.

## Implementation status

Already working:

- `Compile`, `All` and `Get`
- `Asset`, including verified `Read` and `Open`, though only `Load` will create one
- `Ref` and `ParseRef`
- `Embedded`
- `compile.Text` and `compile.JSON`
- `LoadError`, `bundletest.CheckSample` and `bundletest.RequireCode`
- `internal/digest.New` and `Sum`, which hash one file

Still to build, in a suggested order:

1. [ ] `internal/manifest.Parse`: choose the YAML library and implement strict parsing, including the `assets` rules.
2. [ ] `compile.YAML`, sharing that strict parsing.
3. [ ] `internal/snapshot`: `Take` and `FS`, hashing assets as streams.
4. [ ] `internal/digest`: `Bundle` and `Catalog`, with golden tests.
5. [ ] `internal/link.Resolve`.
6. [ ] `Load`, running the stages above and creating each `Asset`, then remove `ErrNotImplemented`.
7. [ ] `Dir`, using `os.OpenRoot`, reopened for each asset read.
8. [ ] `Catalog.Explain`.
9. [ ] Tests that load each `testdata` bundle, and expected output for the package example.
10. [ ] Port a real script kind and intel pack from `cclab-platform` before calling the API stable.

## Checks

```sh
go test ./...
```
