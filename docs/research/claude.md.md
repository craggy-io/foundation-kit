# cclab-platform: Architecture Redesign Research Report

Nov 5, 2026 · @Phil

The platform does not need another refactor of `core/application`; it needs one packaging unit. Today a feature is smeared across six places (a domain package, an adapter file, a settings declaration, a groups entry, a Ping line, a `res/` folder with its own loader) and seven different loading mechanisms bring resources in. This report proposes collapsing that into three concepts — **Kind**, **Bundle**, **Plugin** — behind one loader, one manifest and one Go interface, and shows how the same template becomes the standard shape for every future library so an agent can navigate any repo without a glance.

## 1. The codebase today

One package holds a third of the application. Measured on the clone at commit `86ba7fa` (2026-11-05), `src/` is 47,919 lines of non-test Go in 361 files plus 30,549 lines of tests; `src/core/application` alone is 15,204 lines in 56 files (32% of all production code) and carries 18,612 lines of tests. The next largest package, `core/performance`, is 7,044 lines. The Flutter UI adds 57,906 lines of Dart in 135 files.

| Measure | Value | Why it matters |
| --- | --- | --- |
| Production Go, `src/` | 47,919 lines / 361 files | Size of the problem |
| `core/application` | 15,204 lines / 56 files (32%) | The assembly layer is the biggest domain |
| Largest adapter files | `scripts.go` 801, `performance_caps.go` 761, `memory.go` 661, `intel.go` 633, `ghidra.go` 610 | All over the 400-line review trigger in `application/AGENTS.md` |
| `registry.Func(...)` registrations | 188 | Capability surface |
| `...In` / `...Out` adapter structs | 218 | Each hand-written, each with `jsonschema` tags |
| `map*Err` functions | 16 (incl. `mapDomainErr`, `mapJobErr`) | The "one home" rule has 14 exceptions |
| `*Capabilities()` methods appended in `buildRegistry` | 32 | A hand-maintained composition list |
| `open*` service openers | 8, plus 5 inline closures in `Open` | A second hand-maintained list |
| `define*Settings` functions | 11 | A third |
| Names checked by hand in `Ping()` | 15 `Registry.Get("...")` lookups | A fourth |
| Adapter handlers over 60 lines | 28 | Logic still lands in handlers |
| Exported identifiers, `core/application` | 434 | A "glue" package with the widest public surface in the repo |
| `AGENTS.md` guidance | 5 files; 611 lines in the four Go-side ones | Rules carried in prose, enforced by reading |

Two smaller facts point the same way. `go.mod` carries three `replace` directives pointing at `../../Craggy/agent-sdk`, while the root `AGENTS.md` and README say committed `go.mod` must have none (`archtest` checks it, so either CI is red or the check is skipped locally). And `run.ps1` is 883 lines of PowerShell doing build, serve, hot-reload and dependency rolling: the developer loop has its own bespoke machinery too.

None of this is carelessness. The repo is unusually well documented and its rules are good ones. The numbers show that the rules are fighting the structure: `*App` can reach everything, so everything is written against `*App`.

## 2. The loading problem: seven ways in

`res/` holds 55 files in four syntaxes (21 YAML, 19 Starlark, 13 Markdown, 2 JSON) across ten resource kinds, and the platform reaches them through seven unrelated mechanisms. Each kind was added with its own loader because nothing in the codebase says what "a resource" is.

| Resource kind | On disk | Syntax | Loaded by | Overlay / hot reload | Fails startup? |
| --- | --- | --- | --- | --- | --- |
| Agent definitions | `res/agents/team.star` + `*.md` prompts | Starlark `agent()` + Markdown | `agentsdef.DirRoots(resDir).Load()` (agent-sdk) in `application/agents.go` | No | Yes |
| Agents session config | `res/agents-session.yaml` | YAML | `configyaml.Load` + `os.Stat` fallback to defaults, `agents.go` | No | Yes if malformed |
| Skills | `res/skills/<name>/SKILL.md` | Markdown + front matter | Through the agent-sdk loader (referenced by name from `team.star`) | No | Yes |
| Workflows | `res/workflows/*.star` | Starlark `workflow()` | Agent-sdk loader (same root) | No | Yes |
| Data sources | `res/data/sources/*.star` | Starlark `source()` + `collection()` | `dataservices.Open(Options{SourceDirs})` in `application.go` | Hot-reload only while the data scheduler runs | Yes |
| Intel packs | `res/intel/<pack>/pack.yaml` + `topics/**.yaml` + `docs/*.md` + `answer.schema.json` | YAML + JSON Schema + Markdown | `intel.Load(os.DirFS(...))` in `core/intel/load.go` | No | Yes (collects every problem) |
| Gates authority, policies, actors | `res/gates/authority.yaml` (+ overrides) | YAML | `resources.WithOverrides(os.DirFS(...))` from cclab-gates, `gates_ports.go` | Embedded defaults overlaid by host files | Yes |
| Lab scripts (shipped) | `res/scripts/gate-audit.star` | Starlark `script()` | Not loaded from disk at all: scripts live in lab stores; global promotions come from storage (`loadGlobalScripts`) | n/a | n/a |
| Global script capabilities | storage claims (`platform.script-capability@1`) | Starlark source in blobs | `scripts.Capabilities(ctx, Store, ScopeGlobal)` then `Registry.Replace(prefix, ...)` | Replace on promote | Warn only |
| Settings | `var/settings.json` | JSON | `settings.Store`, declared in 11 `define*Settings` functions beside code | Live | Yes |
| Modules (Go) | `src/internal/modules/*` + `assets/` (Dockerfiles, scripts) | Go + `embed.FS` | Hand-wired in `application/<module>.go`, one `*Capabilities()` each | n/a | n/a |
| Resource digests for performance | `res/{agents,skills,workflows,gates,intel,agents-session.yaml}` | any | `performance_config.go` walks and hashes a hard-coded list | n/a | n/a |

Three observations follow from the table.

1. **Five Starlark dialects, five loaders.** `agent()`, `workflow()`, `source()`, `script()` and the gates' definitions are each a Starlark file that calls one constructor and defines `run`/`sync`. They share script-services underneath but each library wrote its own directory walker, naming rule and validation. `starlarkcap.Watch` already exists in capability-services (poll a root, hot-reload one file per prefix, `types.go` beside it) and the platform does not use it for any of them.
2. **Identity is encoded three different ways.** Intel packs take identity from the folder path ("neither is written inside the files"), agents from the `name=` argument, data sources from `source(name=)`, gates from a versioned `name@2` plus a manifest digest. An agent adding a kind has to decide this again.
3. **The one good loader pattern is not the one copied.** `core/intel/load.go` takes an `fs.FS`, collects every problem into one `LoadError`, and is testable with `fstest.MapFS`. The gates overlay (`resources.WithOverrides`) is the right answer to "embedded defaults, host overrides". Neither is used by the other kinds, and `performance_config.go` re-walks the directories with its own hash.

## 3. Where complexity concentrates

The assembly package is large because it is the only place a feature can be *complete*. A domain package cannot register a capability (archtest forbids the registry import), cannot read a setting (settings are declared in `application`), cannot ship a resource (loaders live in `application`) and cannot appear in `Ping`. So every feature necessarily has a second half in `application`, and that half grows: the adapter, the port implementations, the settings, the error table, the groups entry, the Ping line, the resource digest.

**Adding one capability today touches six places.** `core/<domain>` (logic), `application/<domain>.go` (In/Out structs, handler, `map<X>Err`, port impls), `application/groups.go` (prefix → group), `application/application.go` (`open*` step, `Ping` lookups), `application/capabilities.go` (`buildRegistry` append), and often `application/performance_config.go` (resource digest list). The UI then adds a page. Nothing checks that all six were done except `TestEveryCapabilityHasAGroup` and the hand-written Ping.

**The rules are good but they are prose.** `application/AGENTS.md` names the triggers (a file over \~400 lines, a handler over \~60, an `application/<domain>*.go` set larger than `core/<domain>`). On the clone: 17 of the 56 files exceed 400 lines, 28 handlers exceed 60 lines, and `application/performance*.go` (1,400 lines) is a fifth the size of `core/performance` which is fine, but `application/ghidra*.go` (814 lines) is larger than `core/ghidra` (111 lines) and `application/android.go` (574) wires a domain that lives in another repo. `archtest` enforces import direction and `go.mod` hygiene, which is the right tool, but none of the size or shape rules.

**Duplication has a pattern, not just instances.** The anti-pattern table in `application/AGENTS.md` lists seven things that were copied several times before getting one home (env-job lifecycle, error mapping, sentinel errors, claim reads, port implementations). Each was discovered by accident. The pattern is: a cross-cutting concern that every feature needs has no designated owner, so the first feature writes it and the next five copy it. The remaining instances of that pattern today are the loaders (section 2), the `map*Err` tables (16), the `define*Settings` blocks (11), the `*Capabilities()` lists (32) and the `open*` steps (13).

**Hand-maintained lists are the symptom to watch.** `buildRegistry` appends 32 slices by name; `Open` lists 13 steps by name; `Ping` checks 15 registry names by string; `groups.go` lists 28 prefixes; `performance_config.go` lists six `res/` paths. These five lists all enumerate the same set of things (the features) and will drift. In a plugin design every one of them is derived from the plugin list.

**Public surface is inverted.** The glue package exports 434 identifiers; the deepest domain (`performance`) exports 237; `scripts` 77; `labs` 113. Ousterhout's measure of module quality is functionality per unit of interface, and by that measure `application` is the shallowest package in the repo while being the one every change passes through.

## 4. What to keep

The redesign should promote patterns the code already has, not import new ones. Six things are right and become load-bearing in the proposal.

| Keep | Where it is today | Role in the new design |
| --- | --- | --- |
| One capability shape, projected everywhere | `capability-services/registry` (`Capability`, `Func`, `Replace(prefix, ...)`, `Origin`, views, middleware) | The plugin's *public surface to the outside world* is exactly its capability list; nothing else is projected |
| Consumer-owned ports | `intel.Store`, `contract.Store`, the `core/AGENTS.md` rule "declare ports for what you need" | Plugins declare ports; the host resolves them. Unchanged, just made mechanical |
| Sentinel errors mapped once | `ErrNotFound`/`ErrConflict`/`ErrInvalid`/`ErrDenied` + `mapDomainErr(err, errCodes{...})` | Becomes the *only* error mapper; the 14 `map*Err` tables become data on the plugin manifest |
| `fs.FS` loaders that collect every problem | `core/intel/load.go` (`intel.Load(fsys)`, `LoadError.Problems`) | The template for every kind's `Decode`; testable with `fstest.MapFS` |
| Embedded defaults overlaid by host files | `cclab-gates` `resources.WithOverrides(os.DirFS(...))` | Generalised: every bundle root is an overlay stack (library embed → app `res/` → user `var/`) |
| Import rules as a test | `src/internal/archtest/imports_test.go` | Lifted into a reusable library with rules in a file, run in every repo |
| Settings declared beside the code that reads them | `core/settings` (`settings.Int(s, IntSpec{...})`, live `Get`) | Unchanged in spirit; the declaration moves onto the plugin manifest so it is discoverable |
| Hot-reloadable Starlark capability files | `starlarkcap.Load` / `Watch` (one file per prefix, `types.go`, `Origin{Kind:"script"}`) | The loading engine for every Starlark kind, instead of five walkers |

The registry's own design rules (one shape at the boundary; domain libraries do not know the registry; the registry knows neither domains nor transports; Go calls Go in-process; policy is a filter not a fork) are the clearest architectural statement in the whole system and already answer most of the questions this report raises. The platform's problem is that *above* the registry nothing equally crisp exists for "what is a feature" and "what is a resource".

## 5. Design principles worth internalising

These are the ideas the rest of the report applies. Each is stated once, with the system that demonstrates it best, so they can be reused on the next application without re-deriving them.

**Deep modules, measured as functionality per unit of interface.** Ousterhout's test: the benefit of a module is its functionality, its cost is its interface; a shallow module has an interface complicated relative to what it provides and does not help against complexity, and small modules tend to be shallow ([summary](https://elvischidera.com/books/2022-a-philosophy-software-design.html)). The Unix file API (open/read/write/seek/close) hiding files, directories and permissions is his canonical deep module ([review](https://neonvagabond.xyz/a-philosophy-of-software-design.html)). Practical rule: prefer one `Open(Options) (*Service, error)` with a dozen methods over ten packages of two functions each. The `application` package fails this test; `core/intel` passes it.

**One shape at the boundary, mechanically projected.** Caddy identifies every module by a dotted ID whose namespace tells the host which interface to expect (`http.handlers.file_server`), registers it once with `caddy.RegisterModule`, and then unmarshals config into it and calls `Provision` and `Validate` if the module implements them ([Caddy docs](https://caddyserver.com/docs/extending-caddy)). The OpenTelemetry Collector does the same with factories: a `Factories` struct maps each component type name in the config to the factory that builds it, and every component has the same Start/Shutdown lifecycle ([pkg.go.dev](https://pkg.go.dev/go.opentelemetry.io/collector@v0.40.0/component)). The registry already follows this for capabilities; the proposal extends it to resources and plugins.

**A uniform envelope with `kind` and a version.** Kubernetes objects and Backstage descriptors share the root fields `apiVersion`, `kind`, `metadata`, `spec`; the tuple of `apiVersion` and `kind` is enough for a parser to know how to read the rest, and the format draws directly on the Kubernetes object format ([Backstage ADR002](https://backstage.io/docs/architecture-decisions/adrs-adr002)). Kubernetes' `runtime.Scheme` maps GroupVersionKind to Go types and is populated by each API group's `AddToScheme` ([apimachinery](https://github.com/kubernetes/apimachinery/blob/v0.21.0-beta.0/pkg/runtime/scheme.go)). This is the pattern that turns ten loaders into one: the loader reads the envelope, looks the kind up, and hands the body to the kind's codec.

**Consumer-owned ports (hexagonal architecture, done the Go way).** In Go, the consumer defines the interface it needs and the provider satisfies it implicitly, so consumer-defined interfaces *are* the ports and dependencies point inward without a framework ([Go interfaces are ports](https://dev.to/gabrielanhaia/gos-implicit-interfaces-are-its-best-architecture-feature-2h8p)). Keep ports small (one to three methods) and compose by embedding. The repo already does this; the design makes port resolution part of the plugin lifecycle rather than hand-written in `application`.

**Modular monolith: public facade, private everything else.** A module's public API is the explicit contract other modules may call while its model, services and schema stay internal; public APIs are about controlling coupling, not preventing it ([Jovanović](https://www.milanjovanovic.tech/blog/internal-vs-public-apis-in-modular-monoliths)). Spring Modulith's rule is the simplest statement: types in the module's top-level package are its API; anything in a sub-package is internal and a cross-module reference into it is a verification failure ([Spring Modulith intro](https://www.danvega.dev/blog/introduction-to-spring-modulith)). Go gives this for free with `internal/` directories, which the compiler enforces ([Organizing a Go module](https://go.dev/doc/modules/layout)).

**Fitness functions, not prose rules.** An architecture fitness function is an executable check of a structural property (dependency direction, naming, cycle freedom) that turns a rule into a build failure naming the offender ([Jovanović on ArchUnitNET](https://milanjovanovic.tech/blog/architecture-fitness-functions-archunitnet)). `archtest` is one; the size, shape and completeness rules in `AGENTS.md` should become more of them, because an agent reads a failing test far more reliably than a paragraph.

**Explicit composition root over `init()` magic.** Caddy and `database/sql` register in `init()`; the collector builds an explicit `Factories` value. For a codebase edited mostly by agents, the explicit list wins: it is greppable, orderable, testable, and an agent cannot half-register something by forgetting an import. The cost is one line per plugin in `main`, which is the right place for that line.

**Workspaces instead of `replace`.** A `go.work` file lets several modules be developed together without editing their `go.mod` files; the `go.mod` files stay clean and commit-ready while local development keeps its convenience, and in CI each module should build independently from its own `go.mod` ([Go workspaces for monorepos](https://oneuptime.com/blog/post/2026-02-01-go-workspaces-monorepos/view)). This directly replaces the three `replace` directives and most of `update-go-deps.ps1`.

**Agent guidance goes where the code is, and says the counter-intuitive thing.** The openai/codex repository's `AGENTS.md` sets a hard target of modules under 500 lines excluding tests and splits files past roughly 800, a context-window-aware convention; its strongest entries encode conventions an agent would not infer from the code ([case study](https://codex.danielvaughan.com/2026/05/03/anatomy-production-agents-md-openai-codex-repository-case-study/)). Nested `AGENTS.md` files are supported by the major tools and the nearest file to the edited code wins ([guide](https://morphllm.com/agents-md-guide)). The repo already does this well; the change is to make the guidance *shorter* by moving its rules into tests and templates.

## 6. Target architecture: four layers

&#91;embedded content: four layers · imports point down only\]

One rule governs the picture: an arrow points down and never up or sideways. The app imports plugins and the host; a plugin imports the host, the kit and its own domain, and never another plugin; the host imports the kit; the kit and the domain libraries import nothing above them. Today's `core/application` is the host plus every plugin's adapter in one package; today's `core/<domain>` and `internal/modules/*` are the plugins' `domain/` directories without their `plugin.go`. The move is mostly relocation, with one new package (`host`) and one new kit (`bundle` + `kind`) supplying what every feature currently writes for itself.

## 7. The unifying idea: Kind, Bundle, Plugin

Three concepts replace every bespoke loader and every hand-maintained list. Each is a noun the code, the docs and the agents use with one meaning.

**Kind** is a named, versioned type of resource (`agent@1`, `data-source@1`, `intel-pack@1`, `gate@2`, `workflow@1`, `skill@1`, `lab-script@1`). A kind is defined in Go once, by the plugin that owns it, as a *codec*: how to decode its files from an `fs.FS`, how to validate them, how to compute their content digest, and what Go value they become. Kinds are registered in one **kind registry**, the way Kubernetes types are registered in a `Scheme` and Caddy modules in a namespace. Nothing else in the system knows how to read a `.star` or a `.yaml`.

**Bundle** is the one on-disk unit: a directory with a `bundle.yaml` manifest whose envelope is `apiVersion`, `kind`, `metadata` (name, version, description, labels) and `spec` (kind-specific). The files beside the manifest belong to the bundle. A bundle root (`res/`, a library's embedded defaults, a user's `var/bundles/`) is a directory of bundles, and roots stack as an overlay so defaults ship embedded and hosts override by file, as the gates library already does. One loader walks roots, reads envelopes, dispatches to the kind's codec, computes digests, reports every problem at once, and optionally watches for changes. Loading is the same for every kind; only the codec differs.

**Plugin** is the one Go-side unit: a package that exports a single `Plugin` value with a manifest (name, version, the kinds it defines, the ports it requires, the settings it declares, the capability groups it owns) and a `Register(host)` function that installs its capabilities, kind codecs and port implementations into the host. A plugin owns its domain package, its adapters, its default bundles and its tests, in a fixed directory layout. The composition root is a list of plugins. Everything that today is a hand list in `application` (`buildRegistry`, `Open`, `Ping`, `groups.go`, resource digests) is derived from the plugin manifests by the host.

The three relate simply: *a plugin defines kinds; a bundle is an instance of a kind; the host loads bundles through kinds and projects plugins through the registry.*

| Today | Becomes |
| --- | --- |
| `res/agents/team.star` loaded by agent-sdk's `DirRoots` | A bundle of kind `agent-team@1`; codec provided by the agents plugin, built on `starlarkcap`-style load |
| `res/data/sources/*.star` passed as `SourceDirs` to data-services | Bundles of kind `data-source@1`; the data plugin's codec calls data-services' existing parser on the files |
| `res/intel/<pack>/` loaded by `intel.Load` | Bundles of kind `intel-pack@1`; `intel.Load` *is* the codec, nearly unchanged |
| `res/gates/authority.yaml` + `WithOverrides` | Bundle of kind `gate-authority@1`; the overlay moves into the bundle loader and every kind gets it |
| 11 `define*Settings` blocks | `Settings []settings.Spec` on each plugin manifest; the host declares them |
| 32 `*Capabilities()` + `buildRegistry` appends | `Register` on each plugin; the host appends |
| `groups.go` | `Groups []registry.Group` on each manifest |
| `Ping` string lookups | The host checks every manifest's declared capabilities are registered |
| `performance_config.go` resource walk | The bundle loader already has every bundle's digest |
| 16 `map*Err` tables | One `mapDomainErr` plus an `Errors errCodes` table on the manifest where a plugin needs extra codes |

This is not a new framework so much as naming what exists. `registry.Capability` is already the one shape for behaviour; `Bundle` becomes the one shape for data; `Plugin` the one shape for a feature.

## 8. One on-disk format: the bundle

Every resource is a directory with a manifest. The manifest is YAML because humans edit it; the envelope is the Kubernetes/Backstage one because it is the most widely understood way to say "what kind of thing is this and which version of its schema". The body of a bundle keeps whatever syntax its kind needs (Starlark for behaviour, Markdown for prompts, JSON Schema for contracts), so no existing file has to be rewritten; only its *addressing* changes.

```yaml
# res/agents/lab-team/bundle.yaml
apiVersion: craggy.io/v1
kind: agent-team
metadata:
  name: lab-team            # identity; unique within the kind
  version: 3                # content revision; bump on change
  description: Shipped agents for CC Lab labs
  labels: {owner: platform}
spec:
  entry: team.star          # the kind decides what spec means
  prompts: [role.md, conduct.md, investigation.md, lab.md]
```

Rules that apply to every bundle, enforced by the loader and not by each kind:

1. **Identity is the manifest's `metadata.name`, never the folder name and never a value inside a body file.** Folder names are free; a renamed folder changes nothing. (Intel packs today take identity from the path, agents from an argument, gates from a versioned string: three conventions become one.)
2. **The kind's `apiVersion` + `kind` selects the codec.** A manifest whose kind nobody registered is an error that names the missing plugin, not a silent skip.
3. **Digest is computed by the loader** over the manifest and every file the codec declares it read, so performance measurement, caching and hot reload all use one content address. (`starlarkcap.Load` already computes exactly this for Starlark, including `load()` dependencies.)
4. **Roots overlay.** A root is an `fs.FS`; roots are stacked lowest-priority first (library `embed.FS` → app `res/` → user `var/bundles/`). A bundle in a higher root with the same `kind` + `name` replaces the lower one whole. There is no per-file merging, which is what makes the result explainable.
5. **Every problem is collected, then reported once**, in the `intel.LoadError` style. A broken bundle fails startup and `-check`, as today, but the report lists all of them.
6. **A bundle may reference another only by `kind/name`**, never by path (`skill/lab-scripts`, `data-source/nvd`). The loader resolves references after all roots are read and reports dangling ones.
7. **Optional `bundle_test.star` or `bundle_test.go` beside the manifest** is run by the standard test harness (`starlarkcap.Test` exists for Starlark kinds).

How today's `res/` maps onto this, kind by kind:

| Today | Kind | Bundle body | Codec built on |
| --- | --- | --- | --- |
| `res/agents/team.star` + `*.md` | `agent-team` | `team.star`, prompt `.md` files | agent-sdk's definition loader, given an `fs.FS` instead of a dir |
| `res/agents-session.yaml` | `agent-session` | the YAML (or the manifest's `spec` directly) | `configyaml` |
| `res/skills/<name>/SKILL.md` | `skill` | `SKILL.md` | front-matter parse; already a de facto bundle |
| `res/workflows/*.star` | `workflow` | one `.star` each | agent-sdk workflow loader |
| `res/data/sources/*.star` | `data-source` | one `.star` each | data-services' `source()` dialect, fed one file at a time |
| `res/intel/<pack>/` | `intel-pack` | `topics/**.yaml`, `docs/*.md` | `intel.Load` on the bundle's sub-FS |
| `res/intel/answer.schema.json` | `intel-answer-schema` (or part of the plugin's embedded defaults) | the JSON Schema | — |
| `res/gates/authority.yaml` | `gate-authority` | the YAML | cclab-gates' parser |
| cclab-gates embedded definitions, policies, actors | `gate`, `security-policy`, `gate-actor` | as the library ships them | cclab-gates; the library's `embed.FS` becomes a root |
| `res/scripts/*.star` | `lab-script` | one `.star` | script-services; the host imports them into the lab store on first start instead of ignoring them |
| module `assets/` (Dockerfiles, worker scripts) | `env-profile` | `Dockerfile`, scripts | environments' `envspec.Profile{Build{Context}}` |

The migration cost per kind is a `bundle.yaml` beside existing files and a small codec adapter; no body file changes. Agents writing new resources get one question answered for them ("where does the name go, and how do I say what this is?") and the loader tells them exactly what is wrong when they get it wrong.

## 9. One loading path: the kind registry and bundle loader

The whole loading subsystem is two small packages with one entry point each. Sketched here as Go so the interface can be judged for depth: a lot of behaviour (overlay, digesting, validation, reference resolution, watching, error collection) sits behind a handful of exported names.

```go
// package kind — what a resource type is. Owned by whichever plugin defines it.
package kind

type ID struct{ Group, Name string; Version int }   // "craggy.io", "agent-team", 1

// Codec turns one bundle's files into a Go value. T is the plugin's own type.
type Codec[T any] interface {
    ID() ID
    // Decode reads the bundle (manifest already parsed into m) from its own
    // fs.FS. It returns the value and the files it read, so the loader can
    // digest exactly those. It collects problems rather than stopping at the first.
    Decode(ctx context.Context, m Manifest, fsys fs.FS) (T, []string, error)
    // Validate runs after every bundle of every kind is decoded, so cross-bundle
    // references (skill/lab-scripts) can be checked. Optional: return nil.
    Validate(ctx context.Context, v T, refs Resolver) error
}

// Registry maps ID to codec. One per host. Plugins add to it in Register.
type Registry struct{ /* ... */ }
func (r *Registry) Add(c AnyCodec) error
```

```go
// package bundle — the one loader. Knows nothing about any kind.
package bundle

type Manifest struct {
    APIVersion string            `yaml:"apiVersion"`
    Kind       string            `yaml:"kind"`
    Metadata   Metadata          `yaml:"metadata"`
    Spec       map[string]any    `yaml:"spec"`     // handed to the codec unparsed
}

type Loaded struct {
    Ref    string   // "agent-team/lab-team"
    Kind   kind.ID
    Digest string   // sha256 over manifest + files the codec read
    Root   string   // which overlay root supplied it (for `explain`)
    Value  any      // the codec's T
}

type Options struct {
    Roots []fs.FS          // lowest priority first; embed.FS, os.DirFS(res), os.DirFS(var/bundles)
    Kinds *kind.Registry
    Watch time.Duration    // 0 = load once
    OnChange func(ref string, l Loaded, err error)
}

// Load walks every root, reads every bundle.yaml, decodes each through its
// kind's codec, resolves references, and returns either every bundle or a
// *LoadError listing every problem. It never returns a partial catalogue.
func Load(ctx context.Context, o Options) (*Catalogue, error)

type Catalogue struct{ /* ... */ }
func (c *Catalogue) Of[T any](k kind.ID) []T          // typed read for a plugin
func (c *Catalogue) Get(ref string) (Loaded, bool)
func (c *Catalogue) Digests() map[string]string      // ref -> digest, for performance/config identity
func (c *Catalogue) Explain(ref string) string       // which root won and why
```

What this buys, concretely:

- **`intel.Load` becomes `intelCodec.Decode`** with almost no change: it already takes an `fs.FS` and collects problems. Agents, workflows, data sources and gates each get a codec of 30–100 lines that calls the library's existing parser.
- **Overlay is written once.** The gates library's `WithOverrides` logic moves into `bundle` and every kind gets "embedded defaults, host files override" for free. Library repos ship their defaults as an `embed.FS` root that the platform passes in `Roots`.
- **Hot reload is written once.** `Watch` polls roots (as `starlarkcap.Watch` does) and calls `OnChange`; a plugin that supports live replacement (data sources, global scripts, agent definitions) implements it; one that does not (gates authority) ignores the callback and takes the change at restart. Today only data sources reload, and only while the scheduler runs.
- **Digests are free.** `performance_config.go` stops walking `res/` with its own hash and reads `Catalogue.Digests()`.
- **One error report.** `platformd -check` prints every broken bundle across every kind in one run.
- **One test harness.** `bundletest.Load(t, fstest.MapFS{...})` tests a codec in isolation; `bundletest.Roots(t, embedFS, "res")` asserts the shipped tree loads clean. No `App` is opened to test a resource.

Starlark kinds should use `starlarkcap.Load`/`Watch` semantics where the bundle defines capabilities (lab scripts, promoted scripts) and script-services' plain `Exec`/`Check` where it defines something else (data sources, agents, workflows). Either way the codec, not the plugin, decides; the host sees only `Decode`.

**Where the packages live.** `kind` and `bundle` know nothing about labs, agents or security, so they belong in a craggy-io library beside `capability-services` and `script-services`, not in cclab-platform (section 12).

## 10. Plugins: the Go-side unit

A plugin is a package that exports one value and follows one directory layout. The host does everything else: ordering, port resolution, settings declaration, capability registration, group registration, readiness, shutdown. The interface is deliberately small so that an agent writing its tenth plugin writes it exactly like its first.

```go
// package host — the one interface a feature implements.
package host

type Manifest struct {
    Name        string            // "memory", "intel", "apk"
    Version     int
    Description string
    Kinds       []kind.AnyCodec   // resource kinds this plugin defines
    Requires    []string          // ports it needs, by name: "labs.store", "agents.runner"
    Provides    []string          // ports it offers to other plugins: "artifact.run"
    Settings    []settings.Spec   // declared here, read via h.Setting(key)
    Groups      []registry.Group  // capability groups it owns (prefixes)
    Capabilities []string         // refs it promises to register; the host checks
    Errors      map[error]registry.Code // extra sentinel -> code rows for mapDomainErr
}

type Plugin interface {
    Manifest() Manifest
    // Register is called once, after every plugin in Requires has registered.
    // It installs capabilities, port implementations and codecs through h.
    Register(ctx context.Context, h *Host) error
}

// Optional lifecycle, as the OpenTelemetry Collector's Component.
type Starter interface{ Start(ctx context.Context) error }
type Stopper interface{ Stop(ctx context.Context) error }
type Checker interface{ Check(ctx context.Context) error }   // feeds platformd -check
```

```go
// What a plugin can reach. Deliberately not "everything".
type Host struct{ /* ... */ }
func (h *Host) Register(caps ...registry.Capability) error      // projected everywhere
func (h *Host) Provide(port string, impl any)                   // offer a port
func Port[T any](h *Host, name string) T                        // take a port declared in Requires; panics otherwise
func (h *Host) Bundles() *bundle.Catalogue                      // read resources of your kinds
func (h *Host) Setting(key string) settings.Handle              // a setting you declared
func (h *Host) Events() *events.Hub
func (h *Host) Log() *slog.Logger
```

**The fixed layout.** Every plugin directory looks the same; a reader (or an agent) knows where to look before opening it.

```text
plugins/memory/
  plugin.go          var Plugin host.Plugin = plugin{}  + Manifest()  (≤80 lines)
  caps.go            registry adapters: In/Out types, handlers that call the domain (≤400 lines; split caps_*.go by capability group)
  ports.go           the interfaces this plugin REQUIRES (consumer-owned) and the impls it PROVIDES
  settings.go        settings.Spec values referenced from the manifest
  errors.go          sentinel errors + the Errors table
  kinds.go           kind codecs, if it defines resource kinds
  domain/            the pure domain (today's core/memory), no registry, no host import
  internal/          anything else private to the plugin
  res/               embedded default bundles (go:embed), one directory per bundle
  README.md          what/inputs/outputs/runtime per capability (already the module convention)
  AGENTS.md          ≤40 lines: the counter-intuitive rules only
  *_test.go          domain tests in domain/; adapter + registration tests here, through hosttest
```

**The composition root shrinks to a list.**

```go
// cmd/platformd/main.go
app, err := host.Open(ctx, host.Options{
    VarDir: *varDir,
    Roots:  []fs.FS{os.DirFS(*resDir)},            // library embed roots are added by their plugins
    Plugins: []host.Plugin{
        labs.Plugin, events.Plugin, settings.Plugin, activity.Plugin,
        agents.Plugin, chat.Plugin, contract.Plugin, intel.Plugin,
        scripts.Plugin, memory.Plugin, gates.Plugin, findings.Plugin,
        data.Plugin, environments.Plugin, performance.Plugin, feedback.Plugin,
        apk.Plugin, android.Plugin, binary.Plugin, code.Plugin, ghidra.Plugin,
        fuzz.Plugin, ingest.Plugin, web.Plugin,
    },
})
```

The host orders plugins topologically by `Requires`/`Provides`, fails fast on a missing port or a cycle, declares every setting, loads every bundle root through every registered kind, calls `Register` in order, checks that each manifest's promised `Capabilities` and `Groups` exist (replacing `Ping` and `TestEveryCapabilityHasAGroup`), runs `Start` in order and `Stop` in reverse. `labScope`, `announceChanges`, `recordActivity`, `measureCalls`, `experimentGuard` stay exactly as they are: registry middleware installed by the host, or by a plugin that owns the concern (`labs` installs `labScope`).

**What this does to the 15,204 lines.** The domain packages move intact (`core/memory` → `plugins/memory/domain`). Each `application/<domain>.go` becomes `plugins/<domain>/caps.go` with the same handlers, minus the parts the host now does: `open*`, `define*Settings`, `map<X>Err` tables, group entries, Ping lines. The port implementations (`intelStore`, `contractStore`, `reproducerRunner`, ...) move to `ports.go` of the plugin that *provides* them. `application.go`, `capabilities.go`, `groups.go`, `settings.go` and `performance_config.go`'s resource walk disappear into `host`. Rough estimate from the file sizes: `application` goes from 15.2k lines to a `host` package of 1.5–2k lines, with the remaining \~12k redistributed into plugins where each file sits beside the domain it adapts.

**Cross-plugin calls.** In-process Go still calls Go (registry rule 5). A plugin that needs another's behaviour declares a port in its own `ports.go` and lists it in `Requires`; the providing plugin implements that interface (or the host adapts its facade to it). This is the modular-monolith "public API facade" rule with the compiler enforcing it: a plugin package imports `host`, `registry`, its own `domain/` and the kit libraries, and *nothing* under another `plugins/*`. That one import rule, added to `archtest`, is the whole boundary.

## 11. Vocabulary

Twelve words, each with one meaning, used as package names, file names, manifest fields and in every `AGENTS.md`. The current docs use "module", "adapter", "service", "library", "pack", "profile", "domain" and "capability adapter" with overlapping meanings; the point of a fixed vocabulary is that an agent reading `plugins/intel/ports.go` knows what it will find before opening it.

| Word | Means | Lives in | Not to be confused with |
| --- | --- | --- | --- |
| **Kit** | A generic library with no knowledge of CC Lab: registry, scripts, bundles, host, storage primitives | `github.com/craggy-io/*` | A library that knows the product (that is a *domain library*) |
| **Domain library** | A reusable library about one subject (storage, classify, environments, gates, agents) with a Go API and no registry import | `github.com/philcantcode/cclab-*` | A plugin (which adapts it) |
| **Host** | The one package that opens plugins, loads bundles, builds the registry, orders lifecycle | kit: `host` | `application` (retired word) |
| **Plugin** | One feature: manifest + `Register`; owns a domain, its capabilities, its kinds, its default bundles | `plugins/<name>/` | "module" (retired word) |
| **Domain** | The pure logic of a plugin: entities, rules, flows, queries, sentinels; imports no registry, no host | `plugins/<name>/domain/` | The plugin as a whole |
| **Capability** | One named, versioned, schema'd operation projected to every surface | `registry.Capability`, defined in `caps.go` | A Go method (not projected) |
| **Port** | A small interface a plugin declares for what it needs from elsewhere; the consumer owns it | `plugins/<name>/ports.go` | A Go API a library exports |
| **Facade** | The implementation a plugin *provides* for others' ports, registered with `h.Provide` | `ports.go` of the provider | Importing another plugin's package (forbidden) |
| **Kind** | A versioned resource type with a codec (`agent-team@1`) | `kinds.go` of the owning plugin; registered in `kind.Registry` | A classify type (`android.apk`, which is a *subject type*) |
| **Bundle** | A directory with `bundle.yaml`; one instance of a kind | any root: `res/`, embedded `res/`, `var/bundles/` | A "pack" (retired word; intel packs are bundles of kind `intel-pack`) |
| **Root** | An `fs.FS` of bundles; roots stack as an overlay | `host.Options.Roots` | A repository root |
| **Projection** | A mechanical rendering of the registry to one surface: HTTP, MCP, tools, Starlark | kit: `capability-services/{httpapi,mcp,tools,starlarkcap}` | A plugin's UI page |
| **Composition root** | The one `main` that lists the plugins | `cmd/<app>d/main.go` | The host (which is a library the root calls) |
| **Setting** | A declared, live, user-editable value a plugin reads | manifest `Settings`, `settings.go` | A bundle (resources are authored; settings are tuned) |
| **Subject** | An opaque content type a capability accepts or emits (`android.apk`) | `registry.Subject` | A kind |

Naming rules that follow: packages are the vocabulary word or the plugin name, never both (`plugins/memory`, not `plugins/memorymodule`); capability names are `<plugin>.<verb-or-noun>` with the plugin's name as the first segment (`memory.recall`, `apk.decompile`); kind names are `<noun>[-<noun>]` singular (`agent-team`, `data-source`); file names inside a plugin are the fixed set from section 10; sentinels are the four (`ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrDenied`) plus `ErrUnavailable`, and nothing else without a manifest `Errors` row.

Retire "module" (it means a Go module, a Caddy module and `src/internal/modules` at once), "application" (the package goes away), "pack" (becomes a kind) and "adapter" as a noun for a place (keep it as a verb: `caps.go` *adapts* the domain to the registry).

## 12. Repository split

Fewer, better-shaped repos, not more. You already run ten (platform, six `cclab-*`, three `craggy-io`). The split below adds two generic kit repos, folds the tool plugins into one repo, and leaves the platform as a thin composition root plus the plugins that only make sense inside CC Lab. The test for each boundary: *could someone build a different application with it without reading about labs?* If yes it is kit or domain library; if no it is a plugin or the app.

&#91;embedded content: repositories after the split · arrows are allowed imports\]

Arrows are the only imports allowed; every arrow points down or, within a row, from the consumer to what it consumes. The platform imports everything; nothing imports the platform.

| Repo | Layer | Contains | May import | Why separate |
| --- | --- | --- | --- | --- |
| `craggy-io/capability-services` | Kit | registry, jobs, projections, starlarkcap (as today) | stdlib, jsonschema, script-services | Already generic and public |
| `craggy-io/script-services` | Kit | Starlark dialect, types, modules, checks (as today) | stdlib, starlark-go | Already generic |
| `craggy-io/bundle-services` **(new)** | Kit | `kind`, `bundle`, `bundletest`: manifests, codecs, overlay roots, digests, watch, error collection | stdlib, yaml | The one loader; useful to any app that ships authored resources |
| `craggy-io/host-services` **(new)** | Kit | `host`, `hosttest`, `settings`, `events`, `paging`, `archrules` (the archtest engine with rules in a file) | capability-services, bundle-services | Plugin lifecycle and the cross-cutting services every app repeats; this is what makes the next app fast |
| `craggy-io/data-services`, `agent-sdk` | Kit | as today | script-services | Already generic |
| `cclab-storage`, `cclab-classify`, `cclab-environments`, `cclab-gates`, `cclab-agents` | Domain library | as today; each gains an embedded `res/` root of default bundles and a `kinds` sub-package where it owns a resource kind | kit repos, never host or platform | Already separate; the import rule is unchanged |
| `cclab-plugins` **(new, or fold into platform)** | Plugin | the tool plugins: `apk`, `android`, `binary`, `code`, `ghidra`, `fuzz`, `ingest`, `web`, `signals` | host-services, registry, domain libraries, envjob | They are the "analysis tools" product line, have their own Docker assets and release cadence, and are the most likely to be reused in a non-lab app (a CLI, an MCP server) |
| `cclab-platform` | App | `cmd/platformd`, `internal/api`, the lab-bound plugins (`labs`, `chat`, `contract`, `intel`, `scripts`, `memory`, `findings`, `performance`, `feedback`, `activity`, `policy`), `res/`, `ui/` | everything above | The composition root and what only exists inside CC Lab |

**Where `core/*` lands.** `labs`, `sessions`, `policy`, `envjob`, `artifactbridge`, `events`, `paging`, `settings`, `activity`, `feedback` are candidates for the host or kit because other apps need them; `chat`, `contract`, `intel`, `memory`, `scripts`, `findings`, `performance`, `next`, `ghidra` (the transaction glue) are plugins of the platform. The decision rule for each: strip the word "lab" from its doc comment; if it still makes sense, it is not platform-specific. `events`, `paging`, `settings`, `activity` and `feedback` pass immediately; `labs` is the product's tenancy model and is the plugin that installs the `labScope` middleware; `envjob` belongs beside `cclab-environments` as its `envjob` sub-package.

**Development without `replace`.** Keep `C:\Code\cclab` and `C:\Code\Craggy` as they are and add one uncommitted `go.work` above them listing every module; `go build` in any repo then resolves siblings locally while every committed `go.mod` stays pinned. CI builds each module alone from its `go.mod`. This removes the three `replace` lines, most of `update-go-deps.ps1`, and the "commit and push before the dependent can see it" step for local iteration (publishing is still needed to pin a version).

**What not to split.** The Flutter UI stays in the platform repo: it is 58k lines bound to the platform's capability surface and has no second consumer. Do not create one repo per tool plugin; nine small repos cost more in version pinning than they return, which is why `cclab-plugins` is one repo with one `go.mod` and nine plugin directories. If the tool plugins later need independent release, each directory can become a module in place without moving files.

**Option B: no new repos.** If two new repos feel heavy, `host` and `bundle` can start as packages inside `capability-services` (it already owns `starlarkcap`'s load/watch machinery) and the tool plugins can stay under `cclab-platform/plugins/`. The structure of the code is identical; only `go.mod` boundaries differ, and those can be drawn later with `git filter-repo` once the shapes have settled. The layering and import rules matter more than the repository count.

## 13. The reusable library template

Every craggy-io and cclab repo should have the same skeleton, so the question "where is X?" has the same answer in all of them. The template is small enough to hold in your head and specific enough that an agent can be told "this is a standard library repo" and need nothing else.

```text
<repo>/
  go.mod                     module github.com/<org>/<name>; no replace; one major version
  README.md                  what it is, 10-line quick start, the public interfaces list, link to docs/design.md
  AGENTS.md                  ≤60 lines: what belongs here, what does not, import rules, the one check command
  CHANGELOG.md               keep-a-changelog; one line per published version
  Makefile | check.sh        `make check` = gofmt + vet + build + test + archrules, identical in every repo
  arch.yaml                  import rules for archrules (the archtest engine, as data)
  doc.go                     the package doc: purpose, mental model, the 5–8 exported names that matter
  <name>.go                  Open/New + Options + the Service type (the deep module's front door)
  errors.go                  the sentinels: ErrNotFound, ErrConflict, ErrInvalid, ErrDenied, ErrUnavailable
  ports.go                   interfaces this library needs from its host (consumer-owned), nothing else
  internal/                  everything not in the public surface
  <name>cap/                 optional: the registry adapter (imports <name> and registry only)
  <name>test/                fakes, fixtures, a conformance Run(t, impl) for ports and codecs
  kinds/                     optional: kind codecs for the resource types this library owns
  res/                       optional: default bundles, embedded (go:embed) and exposed as Root() fs.FS
  example_test.go            runnable examples for the front door; compiled by go test
  docs/design.md             the rules (section 1), the API (section 2), what changed since (last section)
```

**Public-interface rules** (these are what make the module deep):

1. One front door: `Open(ctx, Options) (*Service, error)` or `New(Options) *Service`, and `Close`. Options is a struct with documented zero values, never a builder and never more than one constructor.
2. The exported surface fits in `doc.go`: list every exported name there with one line each. If the list exceeds roughly 25 entries, the package is two packages or has leaked internals. (For comparison, `core/application` would need 434 lines.)
3. Errors are the five sentinels wrapped with `%w`, carrying message text the caller can show. No transport codes, no library-specific error types unless they carry data the caller must act on.
4. Ports are declared in `ports.go`, one to three methods each, named for the role (`BlobStore`, `Clock`, `Runner`), and are the *only* interfaces the package exports. Return structs, accept interfaces.
5. The `*cap` adapter is the only package that imports the registry; the `*test` package is the only one that exports fakes. Neither is imported by the core.
6. Resource kinds the library owns live in `kinds/` and its default bundles in `res/`, exposed as `Root() fs.FS`. The library never reads the host's filesystem itself.
7. `arch.yaml` states the import rules in data; `make check` fails on violation in every repo the same way.
8. No `init()` registration. The host lists what it uses.

**Why this makes the next application faster.** With `host-services` and `bundle-services` as kit, a new application is: `cmd/<app>d/main.go` listing plugins, `res/` of bundles, `plugins/` for whatever is specific, and the UI. The APK/Ghidra/web tool plugins compose into an MCP server or a CLI with a different `main` and no lab plugin; a different product can reuse `agents`, `scripts`, `memory` and `data` with its own tenancy plugin instead of `labs`. That is the "systems that compound" property: each plugin written against `host.Plugin` is an asset for every future composition root, and every kind codec makes its resource type loadable everywhere.

**A generator, not a document.** Put the template in a small `craggy new library <name>` / `craggy new plugin <name>` / `craggy new app <name>` command (Go, in `host-services/cmd/craggy`, or a copier/cookiecutter template). Templates are how Backstage and similar platforms make the golden path the easy path; for agents the effect is stronger, because the scaffold itself becomes the specification they fill in rather than a description they interpret. The generator writes the `AGENTS.md`, `arch.yaml`, `doc.go` skeleton and the conformance test stub, so the first commit already passes `make check` and already has the right shape.

## 14. Making it agent-proof

Agents drift where humans drift: toward the file they already have open and the pattern they last saw. The defence is to make the right shape the only shape that compiles and passes, and to keep the prose short. Four mechanisms, in order of leverage.

**1. Rules as tests, with rules as data.** Lift `archtest` into `host-services/archrules` and drive it from an `arch.yaml` per repo. Rules the platform needs today, all mechanical:

```yaml
# arch.yaml (cclab-platform)
layers:
  app:     [./cmd/..., ./internal/api]
  plugins: [./plugins/...]
  kit:     [github.com/craggy-io/...]
  domain:  [github.com/philcantcode/cclab-...]
rules:
  - name: plugins-are-islands
    from: ./plugins/*          # each plugin
    forbid: ./plugins/*        # imports no other plugin
    why: cross-plugin needs are ports declared in ports.go and resolved by the host
  - name: domain-is-pure
    from: ./plugins/*/domain/...
    forbid: [capability-services/registry, host-services/host, ./plugins/*/caps.go]
  - name: only-main-composes
    from: ./plugins/..., ./internal/...
    forbid: ./cmd/...
  - name: no-replace
    gomod: {replace: false}
shape:
  - name: file-size
    glob: "**/*.go"
    exclude: "**/*_test.go"
    max_lines: 500            # the codex repo's number; warn at 400
  - name: plugin-layout
    dir: ./plugins/*
    require: [plugin.go, caps.go, README.md, AGENTS.md, domain/]
    allow:   [ports.go, settings.go, errors.go, kinds.go, internal/, res/, "caps_*.go", "*_test.go"]
  - name: manifest-complete
    check: every plugin's Manifest.Capabilities are registered, every Group has a capability, every Setting is read
```

The `shape` rules are the ones the current `AGENTS.md` can only ask for. `plugin-layout` is the one that stops a new file called `memory_helpers.go` appearing; `manifest-complete` is what replaces `Ping`.

**2. A generator for every unit.** `craggy new plugin memory` writes the fixed layout with a passing test and a 30-line `AGENTS.md`. `craggy new kind agent-team` writes a codec stub, its `bundletest`, and a sample bundle. `craggy new bundle agent-team lab-team` writes the manifest. An agent asked to "add a plugin" is told to run the generator first; the skeleton then constrains everything it writes.

**3. Shorter guidance, nearer the code.** Root `AGENTS.md` shrinks to: the vocabulary (section 11), the layer diagram, `make check`, and "run `craggy new` before adding a unit". Each plugin's `AGENTS.md` holds only what an agent would not infer: the one counter-intuitive rule about that domain ("a failed script is a result, not a failed call"). Everything that is a rule for *all* plugins lives once, in the generator and `arch.yaml`, not repeated per file. Today's 611 lines across five `AGENTS.md` files should come down by half, and the half that remains should be things no test can check.

**4. Conformance suites for the seams.** `capability-services/conformance` already proves every projection behaves alike. Add `hosttest.RunPlugin(t, p)` (registers the plugin against a fake host and checks the manifest's promises), `bundletest.RunCodec(t, c, fixtures)` (decode, digest stability, every-problem-reported) and `porttest.Run(t, impl)` per port in a domain library's `*test` package. An agent that writes a plugin and runs `make check` gets told, by name, which promise it broke.

**Also worth doing, smaller:**

- A single `explain` capability (`platform.explain`) that prints the plugin list, each plugin's ports and who provides them, every bundle with its winning root and digest, and every setting with its owner. It is the runtime version of the architecture diagram and the first thing an agent should call when confused.
- Make `go run ./cmd/platformd -check` the documented smoke test and have it run `bundle.Load` over `res/` plus every embedded root; a broken resource then fails the same way as a broken import.
- Retire `run.ps1`'s dependency rolling once `go.work` is in place; the developer loop becomes `go run` and `flutter run`.

## 15. Migration plan

Strangler, not rewrite: every phase ships, every phase leaves the app running, and every phase deletes code. Order is chosen so the kit exists before anything depends on it, and so the first plugin migrated is the one whose adapter is already closest to the target shape (`intel`, whose loader and `intelStore` port are the reference pattern in the current docs).

| Phase | Work | Deletes | Done when |
| --- | --- | --- | --- |
| 0. Stop the bleeding (days) | Add `go.work`; remove the three `replace` lines; turn the size and layout rules into `archtest` failures (warn first, then fail) | 3 `replace` lines, most of `update-go-deps.ps1` | `archtest` passes and reports shape violations as a count |
| 1. Kit: `bundle` + `kind` (1–2 weeks) | New `bundle-services` (or packages in capability-services): manifest, codecs, overlay roots, digests, `LoadError`, `Watch`, `bundletest`. Port `intel.Load` as the first codec; add `bundle.yaml` to each intel pack | `performance_config.go` resource walk once every kind is migrated | `platformd -check` loads `res/intel` through `bundle.Load` and the old path is gone |
| 2. Kit: `host` (1–2 weeks) | `host.Plugin`, `Manifest`, `Host`, topological `Register`, settings declaration, manifest completeness check, `hosttest`. Wrap today's `App` so `Open` builds a `Host` and runs plugins alongside the existing `open*`/`buildRegistry` path | nothing yet | A `noop` plugin registers through the host in production |
| 3. First plugin: `intel` (1 week) | `plugins/intel/{plugin.go, caps.go, ports.go, kinds.go, domain/}`; `intelStore` becomes a facade provided by `labs`; `intel.Plugin` listed in `main` | `application/intel.go`, `intel_verification.go`, `openIntel`, the intel Ping lines, the `platform.intel` group entry | Registry listing unchanged; `application` is smaller |
| 4. One plugin per week, resource kinds with them | Order by coupling, loosest first: `feedback`, `settings`, `activity`, `findings`, `memory`, `contract`, `scripts` (+`lab-script` kind), `chat` + `agents` (+`agent-team`, `agent-session`, `skill`, `workflow` kinds), `data` (+`data-source` kind), `gates` + `policies` (+`gate-authority`; gates' embedded `res/` becomes a root), `performance`, `labs` (installs `labScope`), `environments` | the matching `application/<x>*.go`, `define<X>Settings`, `map<X>Err`, groups rows, Ping rows | `application/` holds only what has not moved |
| 5. Tool plugins | `apk`, `apk-decompile`, `binary`, `android`, `code`, `ghidra` (+`env-profile` kind for Docker assets), `fuzz`, `ingest`, `web`, `signals`; decide here whether they go to `cclab-plugins` | `src/internal/modules/*` and their adapters | `application/` is empty and is deleted; `main` is a plugin list |
| 6. Consolidate | Move `events`, `paging`, `settings`, `activity`, `feedback` to `host-services`; `envjob` to environments; `archrules` + `arch.yaml` to every repo; `craggy new` generator; rewrite root `AGENTS.md` to vocabulary + diagram + check | the old `AGENTS.md` paragraphs now enforced by tests | Every repo passes the same `make check`; a new plugin is generated, not hand-made |

**Numbers to watch, each phase.** Lines in `application` (15,204 → 0). Loading mechanisms (7 → 1). Hand-maintained lists (5 → 0; the plugin list in `main` is the one that remains, and it is checked). `map*Err` functions (16 → 1). Places touched to add a capability (6 → 1 directory). `AGENTS.md` lines (611 → \~300). Files over 400 lines (17 → 0, with `archtest` holding it).

**What stays untouched.** The domain packages' logic, the registry and its projections, the lab store layout under `var/`, the HTTP/MCP API surface and therefore the UI. Users and agents see the same capabilities before and after; the capability names and schemas do not change. That is what makes each phase safe to ship: the projection is the contract, and the projection is unchanged.

**Risks.** The topological ordering of plugins can expose hidden circular needs (e.g. `chat` and `contract` today call each other through `App`); each is resolved by a port on one side, and finding them is a benefit. Performance measurement's resource digests change format once when the bundle loader supplies them; record both for one release. The gates library's `WithOverrides` must keep its exact override semantics (file-level, missing files fall back) when generalised, since certificates depend on manifest digests; add a conformance test that the old and new loaders produce identical digests for `res/gates` before switching.

## 16. Trade-offs and alternatives considered

| Decision | Chosen | Rejected | Why |
| --- | --- | --- | --- |
| How plugins register | Explicit list in `main`, topologically ordered by the host | `init()` self-registration (Caddy, `database/sql`); Go `plugin` `.so` loading; hashicorp/go-plugin subprocesses | Explicit is greppable and testable, and an agent cannot half-register by forgetting an import. Dynamic loading buys nothing for a single-binary desktop app and costs build complexity on Windows |
| On-disk resource syntax | Keep each kind's body syntax; unify only the envelope (`bundle.yaml`) | One syntax for everything (all-YAML, all-Starlark, CUE) | Behaviour wants Starlark and prose wants Markdown; forcing one syntax makes the worst case for both. The envelope is where uniformity pays, because it is what the loader reads |
| Envelope format | Kubernetes/Backstage `apiVersion/kind/metadata/spec` | A flat `kind: x` header; front matter inside body files | The four-field envelope is widely understood, extensible without ambiguity, and separates "what is this" from "what does it say" so the loader never parses a body |
| Identity | `metadata.name` in the manifest | Folder name; a `name=` inside the body | One rule for all kinds; folder renames are free; the body parser needs no knowledge of identity |
| Overlay semantics | Whole-bundle replacement by `kind/name` across stacked roots | Per-file overlay (today's gates behaviour); deep-merging YAML | Explainable (`Explain(ref)` names one winner). Gates keep per-file semantics *inside* their own codec where certificates depend on it |
| Hot reload | Loader-level `Watch` with per-plugin opt-in | Always reload; never reload | Data sources and scripts want it; gates authority must not take an unreviewed change at runtime |
| Where the host lives | Separate kit repo (`host-services`) | Inside cclab-platform | The host is exactly the part a second application needs; if it lives in the platform it will grow lab-specific. Option B (packages in capability-services first) is acceptable |
| Repo count | +2 kit repos, +1 plugins repo (optional), 0 per-plugin repos | One repo per plugin; one monorepo | Version pinning cost scales with repo count; the import rules, not the repo boundaries, provide the isolation. A monorepo would simplify day-to-day but loses the kit/domain/app layering as published modules |
| Cross-plugin calls | Consumer-owned ports resolved by the host | A shared `platform` interface every plugin can call; event bus only | A shared interface recreates `*App`. Events are kept for change notification, not for requests |
| Where the UI goes | Stays in the platform repo | Per-plugin UI packages | 58k lines of Dart with one consumer; splitting it buys nothing yet. Capability schemas already drive its forms, which is the right coupling |
| `AGENTS.md` | Shorter, nearer the code, counter-intuitive rules only | Longer, more complete | Rules that can be tests become tests; prose that restates a test is noise an agent must read every time |

**What this design does not solve.** The UI's 135 Dart files have no equivalent of a plugin and will keep growing with features; a later report could examine whether screens can be generated from capability schemas plus a small per-plugin layout file. The audit/scheduler contracts that `docs/README.md` marks as open gates are orthogonal to this structure and remain open. And none of this reduces the inherent complexity of gates, performance measurement or contract turns; it only stops that complexity leaking into every other feature through a shared `App`.

## 17. Sources

Case-study facts come from the clone of `philcantcode/cclab-platform` at `86ba7fa` and `craggy-io/capability-services` (`main`, 2026-11-05): `AGENTS.md`, `src/core/AGENTS.md`, `src/core/application/AGENTS.md`, `src/internal/modules/AGENTS.md`, `res/README.md`, `res/intel/README.md`, `docs/README.md`, `go.mod`, and the Go sources counted with `wc`, `grep` and `awk` as described in section 1; `capability-services/docs/design.md` and `starlarkcap/load.go`, `watch.go`.

- [Extending Caddy: module IDs, namespaces, Provision/Validate lifecycle](https://caddyserver.com/docs/extending-caddy)
- [OpenTelemetry Collector `component` package: factories and lifecycle](https://pkg.go.dev/go.opentelemetry.io/collector@v0.40.0/component)
- [Kubernetes apimachinery `runtime.Scheme`](https://github.com/kubernetes/apimachinery/blob/v0.21.0-beta.0/pkg/runtime/scheme.go)
- [Backstage ADR002: default software catalog file format (the envelope)](https://backstage.io/docs/architecture-decisions/adrs-adr002) and [descriptor format](https://backstage.io/docs/features/software-catalog/descriptor-format)
- [Organizing a Go module (go.dev)](https://go.dev/doc/modules/layout)
- [Go workspaces for monorepos: replacing `replace` directives](https://oneuptime.com/blog/post/2026-02-01-go-workspaces-monorepos/view)
- [Internal vs public APIs in modular monoliths (Jovanović)](https://www.milanjovanovic.tech/blog/internal-vs-public-apis-in-modular-monoliths)
- [Introduction to Spring Modulith: top-level package is the API](https://www.danvega.dev/blog/introduction-to-spring-modulith)
- [Architecture fitness functions with ArchUnitNET (Jovanović)](https://milanjovanovic.tech/blog/architecture-fitness-functions-archunitnet)
- [Go's implicit interfaces are its best architecture feature (consumer-owned ports)](https://dev.to/gabrielanhaia/gos-implicit-interfaces-are-its-best-architecture-feature-2h8p)
- [A Philosophy of Software Design, summary notes (deep vs shallow modules)](https://elvischidera.com/books/2022-a-philosophy-software-design.html) and [review](https://neonvagabond.xyz/a-philosophy-of-software-design.html)
- [Anatomy of a production AGENTS.md: the openai/codex repository](https://codex.danielvaughan.com/2026/05/03/anatomy-production-agents-md-openai-codex-repository-case-study/)
- [AGENTS.md guide: nested files, nearest wins](https://morphllm.com/agents-md-guide)
- [Terraform Plugin Framework: schemas as the shape of resources and data sources](https://developer.hashicorp.com/terraform/plugin/framework/handling-data/schemas) (background for the "one shape per concept" pattern)
