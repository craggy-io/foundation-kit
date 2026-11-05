Architectural complexity in platforms that combine static application code, embedded scripting (Starlark), stateful capability packs, and provenance tracking almost always stems from two root causes: **bespoke loader proliferation** (inventing a new directory layout, parser, and lifecycle for every new asset type) and **horizontal layer ambiguity** (splitting logic across too many conceptual rings where boundaries blur in practice).

Redesigning `cclab-platform` around a **3-Stratum Microkernel Architecture**, a **Single-Grammar Resource Registry ("Everything is a Starlark Unit")**, and a **Fixed 6-File Module Anatomy** eliminates bespoke glue code, shrinks the mental model to a handful of primitives, and makes the codebase trivially predictable for both human developers and parallel AI coding agents.

---

## 1. Root-Cause Diagnosis: Why `cclab-platform` Gets Messy

When a system grows organically around workflows, verifiable questions (`verify.star`), capability subsystems, and investigation state, four structural failure modes emerge:

| Failure Mode | Symptom in `cclab-platform` | Architectural Root Cause |
| --- | --- | --- |
| **1. Bespoke Loader Proliferation** | `res/workflows/`, `res/questions/`, prompts, schemas, and configs each have their own file structure, parser, validation logic, and in-memory representation. | Treating *what a resource does* (workflow vs. question vs. rule) as a storage/loading concern rather than a post-load capability binding concern. |
| **2. Four-Ring Layer Ambiguity** | Unclear whether orchestration belongs in `application/` or `subsystems/`, or where Starlark host bindings live relative to `domain/` and `interfaces/`. | Traditional 4-layer Clean Architecture (`domain` → `application` → `subsystems` → `interfaces`) creates "middle-layer mud" where types are mapped 3–4 times across horizontal boundaries. |
| **3. Definition vs. State Entanglement** | Immutable static assets (Starlark scripts, question specs, tool schemas) get mixed with mutable runtime state (active workspace runs, provenance facts, session caches). | Lack of a strict boundary between **Compile-Time/Load-Time Units** (hermetic, content-addressed, frozen) and **Runtime Execution Contexts** (append-only state transitions). |
| **4. High Agent Context Entropy** | Every subsystem uses different file names (`manager.go`, `handler.go`, `runner.go`, `helper.go`) and export patterns, forcing humans and AI agents to read the whole repo to understand one module. | Missing a rigid structural template and a closed architectural vocabulary. When modules are bespoke, AI agents invent new patterns instead of following an existing contract. |

---

## 2. Simplified Layered Structure: The 3-Stratum Microkernel

Instead of four concentric horizontal rings that slice every feature into scattered fragments, collapse the architecture into **three strict vertical-friendly strata** with a single unidirectional dependency rule: **`surfaces` → `modules` → `kernel**`.

```text
cclab-platform/
├── kernel/                  # STRATUM 0: Pure Engine, Contracts, Universal Registry & Provenance Core
│   ├── unit/                # The Universal Resource Envelope, Starlark Loader & Immutable Registry
│   ├── prov/                # Content-addressed Provenance & Evidence DAG (Entity, Activity, Fact)
│   ├── runtime/             # Hermetic Starlark Host, Sandbox & Capability Binding interface
│   └── contract/            # Core interfaces ONLY (Module, Capability, Verifier, Store)
│
├── modules/                 # STRATUM 1: Self-Contained Vertical Capability Packs (Zero cross-imports)
│   ├── question/            # Verifiable Question & Deterministic Verdict engine
│   ├── workflow/            # Multi-step Investigation Workflow runner
│   ├── agent/               # Agent loop, prompt assembly & tool dispatch
│   └── artifact/            # Artifact ingestion, facet extraction & static/dynamic scanners
│
├── surfaces/                # STRATUM 2: Protocol Adapters & Entrypoints (Pure wiring)
│   ├── cli/                 # Command-line runner & batch verifier
│   ├── daemon/              # HTTP / gRPC / WebSocket server for UI
│   └── mcp/                 # Model Context Protocol / Agent tool server surface
│
├── catalog/                 # UNIFIED ON-DISK RESOURCES: Single format (*.unit.star)
│   ├── questions/
│   ├── workflows/
│   ├── personas/
│   └── policies/
│
└── workspaces/              # RUNTIME STATE ONLY: Ephemeral sandboxes & SQLite/CAS provenance stores

```

### The Three Strata Rules

1. **Stratum 0 (`kernel/`) knows nothing about specific domain features.** It knows how to load and freeze a `Unit`, how to record a cryptographic `Fact` into the provenance graph, and how to execute a sandboxed Starlark function with injected host capabilities. It never imports `modules/` or `surfaces/`.
2. **Stratum 1 (`modules/`) contains vertical capability plugins.** Each folder under `modules/` is an isolated island. **Rule of Iron:** `modules/workflow` is strictly forbidden from importing `modules/question` or `modules/agent`. If two modules need to interact, they do so exclusively through `kernel/contract` interfaces or by resolving a `Unit` from `kernel/unit.Registry`.
3. **Stratum 2 (`surfaces/`) contains zero business logic.** It instantiates the `kernel`, registers the active `modules`, points the `Registry` at the `catalog/` directory, and translates CLI flags or HTTP/WebSocket payloads into `kernel` calls.

---

## 3. Unified Dynamic Data & Resource System ("One On-Disk Format, One Loader")

The biggest lever for simplifying `cclab-platform` is eliminating separate YAML/JSON manifests, prompt files, and script companions (like pairing a `question.yaml` + `prompt.md` + `verify.star` in a bespoke folder) and replacing them with a **single self-contained on-disk format** and a **single 4-stage loading pipeline**.

### Design Choice: Why "Everything is a Starlark Unit" (`*.unit.star`) Wins

You typically have two options for unifying on-disk resources:

* **Option A (Directory Bundle / Frontmatter):** A folder or multi-part file combining a declarative YAML/TOML header (`apiVersion`, `kind`, `metadata`, `spec`) with embedded or sibling `.star` scripts.
* **Option B (Pure Starlark-as-Manifest — Recommended):** Every resource on disk—whether it is a verifiable question, a multi-stage workflow, an agent persona, a static rule, or a configuration profile—is a **single hermetic `.unit.star` file** that invokes a built-in `unit()` constructor.

**Why Option B is transformative for `cclab-platform`:**
Because Starlark is already deterministic, hermetic (no filesystem or network access during evaluation), parallel-loadable, and supports `Freeze()`, **Starlark is strictly a superset of JSON/YAML plus deterministic functions.** By making `.unit.star` your single on-disk format:

* You delete all YAML/JSON/TOML/Markdown-frontmatter parsers and directory-stitching loaders.
* A resource's metadata (`id`, `kind`, `version`), static data (`spec`), and executable deterministic logic (`verify`, `plan`, `transform`) live in **one file, one AST, and one evaluation pass**.
* Shared constants, helper functions, or schema macros can be loaded across resources using Starlark's native, deterministic `load("//catalog/lib/assertions.star", "assert_hash")` mechanism.

### The Single On-Disk Format (`*.unit.star`)

Every dynamic resource in `catalog/` follows the exact same shape:

```python
# catalog/questions/apk_debuggable_check.unit.star
load("//catalog/lib/verdict.star", "pass_if", "fail")

unit(
    api = "cclab/v1",
    kind = "question",
    id = "android.manifest.debuggable",
    meta = {
        "title": "Verify APK Debuggable Flag State",
        "tags": ["android", "manifest", "masvs-resilience"],
    },
    spec = {
        "prompt": "Inspect the decoded AndroidManifest.xml and determine whether android:debuggable is enabled and whether custom application classes override debug detection.",
        "required_evidence": ["manifest_sha256", "debuggable_bool", "entry_class"],
    },
    # Executable hooks are first-class frozen Starlark functions inside the same unit
    hooks = {
        "verify": def(ctx, submission):
            if submission["manifest_sha256"] != ctx.fact("artifact.manifest.sha256"):
                return fail("Manifest hash mismatch against provenance graph")
            return pass_if(
                submission["debuggable_bool"] == False,
                reason = "APK is confirmed non-debuggable with verified provenance hash"
            )
    }
)

```

Whether the file defines a `question`, a `workflow`, an `agent_persona`, or a `scanner_rule`, **the outer envelope (`api`, `kind`, `id`, `meta`, `spec`, `hooks`) never changes.**

### The Single 4-Stage Loading Pipeline (`kernel/unit`)

You write the loader **once** in `kernel/unit`. No module ever touches `os.Open` or parses files again:

```text
[Disk / Embed fs.FS] 
   │  1. DISCOVER & HASH: Walk fs.FS for *.unit.star, compute SHA-256 content digest
   ▼
[Hermetic Starlark Evaluator]
   │  2. EVALUATE & FREEZE: Run file in pure sandbox with only `unit()` predeclared;
   │     call starlark.Value.Freeze() so Spec and Hooks are deeply immutable & thread-safe
   ▼
[Kind Schema Validator]
   │  3. VALIDATE: Look up registered KindSchema for `unit.kind` (e.g., "question");
   │     validate `spec` fields and required `hooks` signatures at load time
   ▼
[Immutable Unit Registry]
      4. INDEX: Store frozen UnitHandle indexed by (Kind, ID, Digest).
         Modules query `registry.Resolve(kind, id)` to get a typed, pre-validated handle.

```

#### How Adding a New Feature Works Under This Model

When you create a new module (say, `modules/taint_policy`), you **do not write a file loader**. The `taint_policy` module simply registers a `KindSchema` with the Kernel at startup:

1. Declares `kind = "taint_policy"`, expected `spec` shape, and required `hooks = ["evaluate"]`.
2. Calls `registry.List("taint_policy")` or `registry.Resolve("taint_policy", id)` at runtime.
3. Invokes `handle.CallHook(ctx, "evaluate", args)` through the Kernel's sandboxed Starlark runner.

---

## 4. The "Zero-Glance" Module Standard & Ubiquitous Vocabulary

To make every module immediately recognizable without reading its implementation—and to ensure AI coding agents (Claude Code, Cursor `/multitask` worktrees, OpenCode) generate uniform, boundary-respecting code—establish a **Fixed 6-File Module Anatomy** and a **Closed Architectural Vocabulary**.

### The Fixed 6-File Module Anatomy

Every folder inside `modules/<name>/` (and every standalone reusable library you build) must contain the **exact same 6 files** with the exact same responsibilities. If a file isn't needed (e.g., a stateless module doesn't need `store`), it is omitted, never renamed:

```text
modules/<module_name>/
├── contract.go       # 1. Public types, Spec structs, Input/Output DTOs, and Error codes. Zero logic.
├── module.go         # 2. Lifecycle wiring: implements kernel.Module (RegisterKinds, RegisterCaps, Start, Close).
├── service.go        # 3. Pure core domain orchestration for this module. Depends only on interfaces.
├── starlark.go       # 4. Starlark host built-ins exposed by this module + KindSchema hook wrappers.
├── store.go          # 5. State persistence / Provenance Graph fact emitters & queries (if stateful).
└── module_test.go    # 6. Deterministic table-driven tests using an in-memory Kernel + mock catalog.

```

*(If written in Rust, the exact same 6 files apply: `contract.rs`, `mod.rs` [lifecycle], `service.rs`, `starlark.rs`, `store.rs`, `tests.rs`.)*

**Why this works so well with AI Agents:**

* **Deterministic Context Windowing:** When instructing an agent to *"Add a new Starlark builtin to the `artifact` module,"* the agent knows without searching the repo that the signature belongs in `modules/artifact/contract.go` and the binding belongs in `modules/artifact/starlark.go`.
* **Zero Naming Drift:** Ban vague catch-all filenames like `utils.go`, `helpers.go`, `manager.go`, `handler.go`, `processor.go`, or `types.go`.
* **Mechanical Enforcement:** A 40-line architectural test in `kernel/arch_test.go` walks `modules/*` during `go test` / `cargo test` and fails the build if any module:
1. Contains a file outside the allowed 6-file list (or an explicit `internal/` subdirectory for complex algorithms).
2. Imports another package under `modules/*`.
3. Exports any type not defined in `contract.go` or `module.go`.



### The Closed Architectural Vocabulary (8 Nouns, 4 Verbs)

Cognitive overload happens when five modules use five different words for the same concept (`WorkflowDef` vs. `QuestionTemplate` vs. `RuleConfig` vs. `AgentManifest`). Enforce a single ubiquitous vocabulary across all code, comments, and prompts:

| Term | Category | Strict Meaning Across All Repos & Modules |
| --- | --- | --- |
| **`Envelope`** | Noun | The raw, unvalidated header/body parsed from a `*.unit.star` file (`api`, `kind`, `id`, `meta`, `spec`, `hooks`). |
| **`Unit`** | Noun | A validated, content-addressed, frozen resource residing in the `Registry`. |
| **`Handle[T]`** | Noun | A strongly-typed reference returned by the `Registry` wrapping a frozen `Unit` and its decoded `Spec` of type `T`. |
| **`Capability`** | Noun | A deterministic host function or tool exposed by a `Module` into the Starlark runtime or Agent sandbox. |
| **`Fact`** | Noun | An immutable, content-addressed provenance assertion (`Subject`, `Predicate`, `Object`, `EvidenceDigest`, `Timestamp`) written to the Provenance Graph. |
| **`Verdict`** | Noun | The deterministic result of executing a verification hook (`Pass`, `Fail`, `Inconclusive` + `EvidenceDigest` list). |
| **`Module`** | Noun | A Stratum-1 plugin implementing the `kernel.Module` interface. |
| **`Surface`** | Noun | A Stratum-2 transport adapter (CLI, HTTP, MCP, UI bridge) that translates external I/O into Kernel calls. |
| **`Register`** | Verb | Bind a `KindSchema` or `Capability` into the `Kernel` at startup (never at runtime). |
| **`Resolve`** | Verb | Fetch an immutable `Handle[T]` from the `Registry` by `(kind, id)` or digest. |
| **`Evaluate`** | Verb | Execute a frozen Starlark hook or capability inside a hermetic `Context`. |
| **`Record`** | Verb | Append an immutable `Fact` or `Verdict` into the Provenance Graph. |

### The Tight Public Interface (`kernel/contract`)

Every module plugs into the kernel via a single, tiny interface—keeping the public surface area minimal:

```go
package contract

import "context"

// Module is the ONLY public entrypoint a Stratum-1 package exposes.
type Module interface {
    Name() string
    RegisterKinds(reg KindRegistrar) error
    RegisterCapabilities(cap CapabilityRegistrar) error
}

// Registrar interfaces passed during boot—modules never hold mutable global state.
type KindRegistrar interface {
    RegisterKind(kind string, schema SpecValidator, requiredHooks []string) error
}

type CapabilityRegistrar interface {
    RegisterCapability(namespace string, fn HostFunc) error
}

// ExecutionContext is passed to every evaluation, giving controlled access to Units and Facts.
type ExecutionContext interface {
    Context() context.Context
    ResolveUnit(kind, id string) (UnitHandle, error)
    QueryFacts(filter FactFilter) ([]Fact, error)
    RecordFact(fact FactDraft) (FactDigest, error)
}

```

---

## 5. Multi-Repo vs. Monorepo Strategy: Building Reusable Foundational Libraries

You want this redesign not just to clean up `cclab-platform`, but to give you **reusable building blocks** so future applications (static analysis pipelines, automated APK/binary triage tools, agentic benchmarks, or game scripting engines) can be assembled in hours.

### What to Separate vs. Keep Together

If you split into 5 separate Git repositories immediately during an active redesign, version-bumping commits (`go get` / `cargo update` across repos) will slow down iteration. Instead, use the **Workspace-First, Split-on-Second-Consumer** strategy:

1. Structure the reusable engines as **strictly isolated libraries under `pkg/**` (using Go Workspaces `go.work` or Cargo Workspaces) that have **zero imports** from `cclab-platform`.
2. Enforce the exact same 6-File Module Anatomy inside each `pkg/<lib>` library.
3. The moment you start a second project that needs one of these libraries, `git subtree split` (or move) that folder into its own repository.

### The 4 Reusable Foundational Libraries to Extract

| Library Name | Role & Responsibility | Reusability in Future Projects | Separate Repo Candidate? |
| --- | --- | --- | --- |
| **`starunit`** | **Universal Starlark Resource Engine.** Walks any `fs.FS`, evaluates `*.unit.star` files hermetically, freezes values, validates `spec` schemas, computes SHA-256 digests, and serves typed `Handle[T]` lookups. | **Universal.** Use in *any* future app that needs plugins, rules, prompts, DSL workflows, config packs, or hot-reloadable definitions without writing parsers. | **Yes — Extract First.** Completely domain-agnostic; zero `cclab` concepts. |
| **`provdag`** | **Deterministic Provenance & Evidence DAG.** W3C PROV-inspired append-only graph (`Entity`, `Activity`, `Agent`, `Fact`) backed by SQLite + Content-Addressed Storage (CAS). | **High.** Reusable across vulnerability research testbeds, APK analysis pipelines, agent audit logs, and scientific/data lineage tools. | **Yes — Extract Second.** Pure graph + cryptographic evidence store. |
| **`capkernel`** | **Microkernel Plugin & Capability Bus.** Manages `Module` registration, dependency boot order, sandboxed host capability dispatch, and structured telemetry. | **High.** Acts as your personal application framework ("Standard Chassis") so every future CLI or daemon starts with the exact same `main.go` wiring. | **Yes — Extract Third.** Becomes your personal app skeleton. |
| **`verdict`** | **Machine-Verifiable Question & Grading Engine.** Pairs open-ended agent/human tasks with deterministic Starlark verification hooks and provenance assertions. | **Medium-High.** Reusable for CTF/cyber ranges, LLM agent evaluation benchmarks, automated compliance checks, and formal verification harnesses. | **Keep in `cclab-platform` initially** (as `modules/question`), extract if building a standalone eval harness. |

With `starunit`, `provdag`, and `capkernel` extracted as reusable libraries, **`cclab-platform` shrinks by ~60%**. It becomes simply a thin composition of domain modules (`question`, `workflow`, `agent`, `artifact`) and a `catalog/` of `.unit.star` files.

---

## 6. Step-by-Step Refactoring Blueprint for `cclab-platform`

To transition `[https://github.com/philcantcode/cclab-platform](https://github.com/philcantcode/cclab-platform)` cleanly without breaking existing functionality all at once, execute the redesign in four sequential phases:

### Phase 1: Build `pkg/starunit` and Unify `res/` into `catalog/`

1. Create `pkg/starunit` implementing the `unit(api, kind, id, meta, spec, hooks)` Starlark predeclared builtin, SHA-256 content hashing, `Freeze()`, and `Registry`.
2. Convert one existing resource type in `res/questions/` (where prompts and `verify.star` are currently split across multiple files) into a single `*.unit.star` file under `catalog/questions/`.
3. Convert one workflow in `res/workflows/` into `catalog/workflows/*.unit.star`.
4. Delete the legacy bespoke loaders for questions and workflows.

### Phase 2: Establish `kernel/contract` and `pkg/provdag`

1. Extract the provenance graph logic into `pkg/provdag` with a strict 3-method interface: `RecordFact`, `QueryFacts`, and `VerifyLineage`.
2. Create `kernel/contract` containing only the `Module`, `KindRegistrar`, `CapabilityRegistrar`, and `ExecutionContext` interfaces.

### Phase 3: Reshape Subsystems into the 6-File `modules/*` Standard

1. Create `modules/question`, `modules/workflow`, `modules/artifact`, and `modules/agent`.
2. Inside each directory, lay down the canonical 6 files (`contract`, `module`, `service`, `starlark`, `store`, `module_test`).
3. Move existing domain and subsystem code into `service` and `starlark`, stripping out any direct cross-module imports and routing all inter-module calls through `ExecutionContext`.
4. Add `kernel/arch_test` to lock in the 6-file rule and forbid cross-module imports permanently.

### Phase 4: Thin Out `surfaces/` and Add `AGENTS.md`

1. Collapse `interfaces/` and `ui/` backend glue into `surfaces/cli` and `surfaces/daemon`.
2. Add a root `AGENTS.md` (less than 100 lines) that documents the **3 Strata**, the **6-File Module Anatomy**, the **8 Sacred Nouns**, and the **`*.unit.star` grammar**. Any AI coding agent spun up in a Git worktree will immediately read `AGENTS.md` and generate code that looks identical to the rest of the platform.

---

## Interactive Architecture Explorer

Use the interactive diagram below to inspect how the **3-Stratum Microkernel**, the **Unified `*.unit.star` Resource Pipeline**, and the **Provenance Graph (`provdag`)** connect across load time and runtime execution: