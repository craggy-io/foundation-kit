Creating a dedicated, zero-domain core repository is one of the highest-leverage architectural moves you can make—**provided you avoid the "Fork Divergence Trap."**

If you build that core purely as a **forked template repo** (or Git submodule/subtree that you copy and modify), the first time you fix a subtle Starlark concurrency bug or improve provenance querying in Project B, Project A and Project C are left behind on an older fork. Conversely, if you build it purely as an **imported library**, it solves code reuse for the engine (`starunit`, `provdag`, `kernel`), but it cannot enforce your folder layout, provide your `AGENTS.md` constitution, or ship your AI coding agent skills to a new repository.

The cleanest solution is the **Dual-Role Chassis Pattern**: a single zero-domain repository (let's call it **`chassis`**) that acts simultaneously as an **importable engine library** AND a **project/module scaffolder** with embedded agent skills.

---

## 1. Library vs. Forked Template vs. Dual-Role Chassis

| Architectural Dimension | Option A: Forked / Subbed Template Repo | Option B: Pure Imported Library | Option C: Dual-Role Chassis Repo (Recommended) |
| --- | --- | --- | --- |
| **How you use it** | Clone/fork the repo and build modules inside it | `go get` / `cargo add` the core packages into a new repo | Import `chassis/pkg/*` as a library + run `chassis init` / `chassis sync` CLI |
| **Core Engine Updates (`starunit`, `provdag`)** | **Painful.** Requires manual cherry-picking or messy upstream merges across forks. | **Seamless.** Bump the dependency version in `go.mod` / `Cargo.toml`. | **Seamless.** Engine lives in `chassis/pkg/*` and updates via version bump. |
| **Folder Structure & Boilerplate** | **Included** on day one, but drifts over time as apps diverge. | **Missing.** You have to recreate directories and wiring manually per app. | **Enforced.** `chassis init` and `chassis module <name>` stamp out the exact 6-file anatomy. |
| **AI Agent Skills & `AGENTS.md**` | Copied once at fork time; improvements don't sync back easily. | Not included (libraries don't ship repo-root agent instructions). | **Synchronized.** `chassis sync` updates `AGENTS.md` and `.claude/` / `.cursor/` skills from embedded templates. |
| **Agent Guardrails** | Agents can accidentally modify the core engine files because they sit in the same repo. | Agents cannot corrupt the engine (it's read-only in the module cache). | **Read-only Engine + Active Linter.** Engine is immutable; exported `archtest` validates the app repo. |

---

## 2. Anatomy of the Zero-Domain `chassis` Repository

By using Go's `//go:embed` (or Rust's `include_dir!`), a single repository can hold your reusable runtime packages alongside your canonical project skeleton and agent skill definitions:

```text
chassis/                               # ZERO domain knowledge allowed in this repo
├── pkg/                               # 1. IMPORTABLE ENGINE LIBRARIES (Read-only to downstream apps)
│   ├── contract/                      #    The 8 Sacred Nouns & 4 Core Interfaces (Module, Capability, etc.)
│   ├── starunit/                      #    Universal *.unit.star loader, SHA-256 hasher, Freeze() & Registry
│   ├── provdag/                       #    Content-addressed Provenance & Evidence DAG (Entity, Activity, Fact)
│   ├── kernel/                        #    Microkernel boot, capability bus & sandboxed ExecutionContext
│   └── archtest/                      #    Exported AST/layout verifier called by downstream repo tests!
│
├── scaffold/                          # 2. EMBEDDED TEMPLATES & AGENT SKILLS (Embedded into CLI binary)
│   ├── embed.go                       #    Exposes templates via embed.FS
│   ├── root/                          #    Repo-level files synced to every downstream application:
│   │   ├── AGENTS.md.tmpl             #      Universal architecture rules (3 Strata, 6 Files, 8 Nouns)
│   │   ├── .claude/commands/          #      Reusable slash-commands/skills for Claude Code / OpenCode
│   │   │   ├── new-module.md          #      Skill: "Implement a 6-file Stratum-1 module from a spec"
│   │   │   └── new-unit.md            #      Skill: "Author and verify a hermetic *.unit.star resource"
│   │   └── .cursor/rules/             #      Cursor worktree / sub-agent boundary enforcement rules
│   └── module/                        #    The canonical 6-file module template:
│       ├── contract.go.tmpl
│       ├── module.go.tmpl
│       ├── service.go.tmpl
│       ├── starlark.go.tmpl
│       ├── store.go.tmpl
│       └── module_test.go.tmpl
│
└── cmd/
    └── chassis/                       # 3. TINY SCAFFOLDER & SYNC CLI (~200 lines of code)
        └── main.go                    #    Commands: `chassis init`, `chassis module <name>`, `chassis sync`

```

---

## 3. Three Mechanisms That Make This Work in Practice

### Mechanism 1: Exported Architectural Enforcement (`chassis/pkg/archtest`)

If you only put `arch_test.go` in a template, someone (or an AI agent) can edit or weaken the test in a downstream repo. Instead, write the architectural verifier **inside `chassis/pkg/archtest**` as an exported library function.

Every downstream application (`cclab-platform`, a future static analyzer, or a game tool) has a 7-line `arch_test.go` at its root:

```go
package main_test

import (
    "testing"
    "github.com/philcantcode/chassis/pkg/archtest"
)

func TestArchitectureCompliance(t *testing.T) {
    archtest.VerifyRepository(t, archtest.Config{
        RootDir: ".",
        EnforceSixFileModules: true, // Fails if modules/<x>/ has non-standard filenames
        ForbidCrossModuleImports: true, // Fails if modules/a imports modules/b
        ValidateCatalogUnits: "./catalog", // Parses and schema-checks all *.unit.star files
    })
}

```

When you tighten an architectural rule in `chassis/pkg/archtest`, every application you maintain inherits that stricter check on its next `go test`.

### Mechanism 2: Read-Only Engine Isolation for AI Agents

One of the biggest hidden problems with putting the core engine inside the same repo as your domain modules is **agent scope creep**. When an AI agent is struggling to make a domain feature work inside `modules/workflow`, it will often try to "fix" the problem by hacking a special case into `kernel/` or the Starlark loader.

When `kernel`, `starunit`, and `provdag` live in `[github.com/philcantcode/chassis](https://github.com/philcantcode/chassis)` and are imported via `go.mod` / `Cargo.toml`:

* The engine source code is **read-only** in the package cache.
* An agent working in a Git worktree *cannot* pollute the core engine with domain-specific hacks; it is physically forced to build against `contract.Module` and `contract.ExecutionContext`.
* During early development, if you *intentionally* want to co-edit `chassis` and `cclab-platform` side-by-side, you simply drop a local `go.work` file (or Cargo `[patch]`) in your parent directory without committing it.

### Mechanism 3: Synchronized Agent Skills (`chassis sync`)

Your agent instructions (`AGENTS.md`, `.claude/commands/`, `.cursor/rules/`) should evolve alongside your core architecture. Because every application you build on `chassis` uses the exact same:

* **3 Strata** (`kernel` [imported from chassis], `modules/`, `surfaces/`)
* **6-File Module Anatomy** (`contract`, `module`, `service`, `starlark`, `store`, `module_test`)
* **1 Resource Format** (`catalog/**/*.unit.star`)

...your agent skills become **100% portable across every project you own**. A skill like `/new-module` doesn't need to know anything about cybersecurity or game development—it knows how to read a prompt, generate the 6 standard files implementing `chassis/pkg/contract.Module`, register its Starlark `KindSchema`, and run `go test ./...` until `archtest.VerifyRepository` passes.

---

## 4. The "Zero Domain Bleed" Gate: What Belongs in `chassis`?

To keep `chassis` rock-solid and prevent it from turning into a dumping ground for miscellaneous utilities, apply three strict admission rules before moving any code into it:

1. **The "Two Weird Apps" Litmus Test:** Ask yourself: *"Could this exact package be used unchanged in both an **Android APK vulnerability research testbed** and an **indie RPG quest/dialogue tooling pipeline**?"*
* `starunit` (loads deterministic `.unit.star` specs and hooks): **Passes.** Both apps need hermetic scripts and declarative definitions.
* `provdag` (append-only DAG of `Entity`, `Activity`, `Fact`, `Verdict`): **Passes.** Both apps track state lineage and verifiable execution traces.
* `capkernel` (plugin registry and capability bus): **Passes.**
* `llm_agent_loop` or `apk_unpacker`: **Fails.** Keep these as portable 6-file Stratum-1 `modules/` that can be copied or imported separately, not in the base `chassis`.


2. **The 8-Noun Vocabulary Ceiling:** Code inside `chassis` may only use the 8 architectural nouns (`Envelope`, `Unit`, `Handle`, `Capability`, `Fact`, `Verdict`, `Module`, `Surface`). If a struct, interface, or variable name requires a domain noun (`Prompt`, `APK`, `Vulnerability`, `Shader`, `Container`), it belongs in a Stratum-1 module, not in `chassis`.
3. **Extract, Don't Speculate:** Build and prove the `kernel/` + `pkg/starunit` + `pkg/provdag` packages inside `cclab-platform` during Phase 1 and Phase 2 of your refactor first. Once the interfaces settle and pass `archtest` cleanly with zero imports back into `cclab-platform`, move them into `[github.com/philcantcode/chassis](https://github.com/philcantcode/chassis)` in a single afternoon.

---

## Interactive Comparison: How `chassis` Changes Multi-App Maintenance

Use the interactive model below to toggle between **Forked Template**, **Pure Library**, and **Dual-Role Chassis** modes and step through how project bootstrapping, parallel agent worktrees, and core engine bug fixes propagate across multiple applications: