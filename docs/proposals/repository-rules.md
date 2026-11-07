# Proposal: repository rules checked by ordinary tests

**Status:** Implemented in `archtest` as `CheckFile`; [ADR-0003](../decisions/ADR-0003-repository-rules-and-shared-internal.md) records the decisions below and the semantics settled during implementation. The [archtest README](../../archtest/README.md) is the reference for the format; where it differs from this document, it wins.  
**Date:** 2026-11-07  
**Owner:** Foundation Kit; concrete policies belong to consuming repositories.

## Purpose

Let a repository express observable structural requirements in a reviewed YAML file and make its normal test command fail when they are violated. An agent moving a file, adding a dependency, or creating a catch-all bundle should receive an actionable failure rather than silently bypass a convention documented in `AGENTS.md`.

Extend the existing `archtest` rather than introduce a competing checker. Its purpose already matches this request; its current checks cover only part of the architectural model. YAML is the primary authoring interface: one proposed root file, `archtest.yaml`, and a stable Go test call. Ordinary policy changes require no Go edits.

Four main rule families cover the initial need: Go dependency boundaries, directory layout, structured-file validation, and Markdown structure. The mechanism is generic enough for application repositories and libraries; product-specific directory names, kinds, schemas, and compilers remain in the consumer. The immediate use is authored-content layout in a consuming application; Foundation Kit's package boundaries and package READMEs provide a second use.

Rules cannot determine whether prose is accurate or whether two contributions conceptually belong together. They can require a heading, a nonempty section, typed metadata, an explicit bundle boundary, and a declared dependency. Review still owns meaning. In this document, the user's phrase “one logical resource” means one consumer-defined distribution represented by a bundle, following [ADR-0001](../decisions/ADR-0001-standard-vocabulary.md); it does not introduce a new content type.

## Current evidence and boundaries

The following was inspected in the owning source on 2026-11-07:

| Existing mechanism | What it already does | Consequence for this proposal |
| --- | --- | --- |
| [`archtest`](../../archtest/archtest.go) | Checks Go package imports with `Name`, `Why`, `From`, `Except`, `Forbid`, and `Allow`; rejects unmatched local package patterns; supports package coverage and rejects `go.mod` replacements by default | Reuse its scanner and matcher; do not implement a second import parser |
| `archtest` scanning | Parses non-test `.go` imports without evaluating build tags; skips `testdata`, `vendor`, dot/underscore directories and nested Go modules; retains the first file for a package/import pair | Preserve this policy initially; richer positions require extending the owning scanner |
| [`bundle.Load`](../../bundle/load.go) and [`pipeline.go`](../../bundle/pipeline.go) | Validate manifests, paths, assets, kinds, compiled schemas, dependencies, references, and overrides; collect deterministic diagnostics | Keep runtime bundle validation here, with real consumer compilers and kinds |
| [`bundle.Children`](../../bundle/source.go) | Lists immediate directories containing a manifest; silently skips loose files, hidden directories, symlinks, and directories without manifests | Discovery is not enforcement of a repository's folder policy |
| [`strictyaml`](../../bundle/internal/strictyaml/strictyaml.go) | Rejects duplicate keys, aliases, anchors, merge keys, tags, non-string keys, and multiple documents; records positions | Share this implementation by a deliberate internal extraction, not a copied decoder |
| [`kind/jsonschema`](../../kind/jsonschema/jsonschema.go) | Compiles schemas without external reference loading and validates JSON-compatible values | Reuse this extension from test support; do not add another schema engine |

The inspected packages expose no general YAML-driven directory or Markdown rule system. A repository layout failure is distinct from a bundle load failure: `res/workflows/bundle.yaml` may be a valid bundle but violate a policy that `workflows` is only a grouping directory. Conversely, having a manifest at the right depth does not prove the bundle loads.

## Start with the architectural model

The simple user experience is: describe where things belong in **one root file**, then run the normal tests. Keep the vocabulary small: select paths, require or forbid children, allow known file shapes, and forbid imports. Do not require users to learn an expression language. The longer semantics below specify predictable behavior for implementers, not extra configuration every repository must write.

Useful adjacent checks are required directories (detect accidental deletion), closed allowances (detect unexpected or unowned files), stale selectors (detect rules that no longer check anything), and narrow exceptions with reasons. These belong in MVP. A full ownership inventory, shared policy inheritance, and an interactive CLI can wait.

## Proposed configuration contract

Place `archtest.yaml` at an explicitly supplied repository root. Version 1 is a single document with `version`, optional `exclude`, and a nonempty `rules` sequence. Every rule has a unique nonempty `id`, a nonempty `why`, and exactly one family payload. Rule IDs are stable diagnostic identifiers. All snippets below are proposed syntax, not supported configuration today.

Reject unknown versions, unknown fields at every depth, duplicate mapping keys or rule IDs, wrong scalar types, empty checks, malformed patterns, and contradictory requirements. Reject YAML aliases, anchors, merges, explicit tags, multiple documents, and non-string keys using the existing strict subset. No environment expansion, implicit includes, templates, shell commands, or network access. Validate the entire configuration before examining repository contents; an invalid configuration cannot produce a passing partial check.

### Paths, selectors, and missing matches

- Paths use `/`, are relative to the explicit root, and are case-sensitive on every OS. Reject absolute paths, drive/UNC prefixes, backslashes, `..`, empty components, and leading `./`; `.` alone selects the root. Import patterns are a separate grammar described below.
- Path globs support `*` (zero or more characters within one component), `?` (one character), and `**` (zero or more whole directory components). No braces, character classes, negation, or regular expressions. `res/**/README.md` includes `res/README.md`; `res/*` selects only immediate children. Dot names match normally.
- `select` has `kind: file|directory`, a nonempty `include` list, optional `exclude`, and `on_empty: error|allow` (default `error`). Evaluate the union of includes minus excludes, deduplicate, then sort. Each include pattern must match after exclusions unless `on_empty: allow`; this catches a misspelled branch even if another include matches. An unused exclude is allowed, because it can reserve a deliberately absent path.
- `on_empty: allow` requires `empty_reason`. It is only for truly optional scopes; it cannot excuse a missing required child of an existing selected directory. Type mismatches never count as matches.
- Root `exclude` removes entries and prunes directories before generic selection. Only `.git` and its descendants are excluded implicitly. Do not inherit `.gitignore`: ignored files may still matter. Every other exclusion is reviewed configuration, with a reason, and is displayed in verbose output. Requirements aimed into an excluded path fail as configuration conflicts rather than pass vacuously.
- Required child patterns match relative to each selected directory and require at least one match per pattern. Forbidden patterns succeed when absent. Allow lists describe permitted names; they do not assert existence. These are deliberately different from selector includes.

### Example: code in src, authored content in res, no embedding

```yaml
version: 1
rules:
  - id: declared-top-level-layout
    why: Keep code, authored content, and documentation in their declared locations.
    select: {kind: directory, include: ['.']}
    tree:
      require:
        directories: [src, res, docs]
      allow:
        files: [archtest.yaml, README.md, AGENTS.md, LICENSE, .gitignore]
        directories: [src, res, docs, .github, var, build]
  - id: source-files-only
    why: Authored content belongs under root res; runtime state belongs under var and generated output under build.
    select:
      kind: directory
      include: [src, 'src/**']
    tree:
      allow:
        files: ['*.go', go.mod, go.sum, README.md, AGENTS.md]
        directories: ['*']
      forbid: [res, assets, data, runtime, generated, build, '*.db', '*.sqlite', '*.log']
  - id: no-go-embedding
    why: Load authored bundles from root res through the bundle pipeline.
    select:
      kind: file
      include: ['src/**/*.go']
    go_source:
      forbid_imports: [embed]
      forbid_directives: [go:embed]
```

This intentionally strict source allow list rejects JSON/YAML/text/binary content anywhere in `src`, including test fixtures, unless the consumer explicitly changes its policy. The closed root list prevents new top-level content directories from silently appearing. Root `var` and `build` are allowed, not required or implicitly ignored. A repository with native source files or other legitimate build inputs must declare them. Directory names alone do not identify content; the file allow list supplies the broader check. The example requires `res` but its bundle structure is governed by the next example.

`go_source` is a small source-policy check, not a general AST query language. It uses the same Go parsing helper as imports, requesting comments when needed. Unlike existing package import checks, its explicit selector includes test files, generated files, nested Go modules, and files behind build constraints. Match the decoded import path `embed` regardless of its local name: normal, blank (`_`), renamed, and dot imports all fail. Parse real line-comment tokens for `//go:embed` followed by whitespace or end of comment, including normally indented directives; string literals, block comments, and `// go:embed` are not directives. Reject directive-shaped comments even if misplaced or their patterns are invalid; compilation still diagnoses syntax and placement. This avoids interpreting an embedding glob just to prohibit embedding. The [Go embed documentation](https://pkg.go.dev/embed) describes valid directives, blank imports for strings/bytes, and package-relative patterns.

These are distinct guarantees. Moving files to `res` does not prohibit embedding; banning `embed` does not place files correctly. Neither proves that all loading uses `bundle.Load`, prevents compiled string literals, or prevents a running program from writing into `src`. Runtime-generated files present when tests run fail the layout rules, but proving that execution never writes there needs consumer integration tests or write-path controls. Generated Go source can satisfy the extension allowance; detecting its provenance is outside this simple checker. Document those limits instead of claiming a complete data-flow proof.

For a legitimate exception, add a rule-local `exceptions` list with literal path and reason, for example `[{path: src/example_test.go, why: This isolated test verifies embedding compatibility.}]`. An exception suppresses only that rule on that exact selected target, must currently match, and is shown in verbose output. For a tree rule the target may be the reported child path. No wildcard exceptions or global warning baseline in MVP. Do not exempt an entire subtree when a single fixture suffices.

### Example: grouping folders and independent leaf bundles

This complete example expresses a consumer policy, not Foundation Kit's own directory layout:

```yaml
version: 1
exclude:
  - path: build/**
    why: Build output is not authored content.
rules:
  - id: content-groups
    why: Keep distributions in named leaf bundles, not loose files or a group-wide bundle.
    select:
      kind: directory
      include: [res]
    tree:
      require:
        files: [README.md, AGENTS.md]
        directories: [workflows, models]
      allow:
        files: [README.md, AGENTS.md]
        directories: [workflows, models]
      forbid: [bundle.yaml]
  - id: content-group-children
    why: Each child directory is one independently loadable distribution.
    select:
      kind: directory
      include: [res/workflows, res/models]
    tree:
      allow:
        files: [README.md, AGENTS.md]
        directories: ['*']
      forbid: [bundle.yaml]
      child_directories:
        min: 1
        require_files: [bundle.yaml, README.md]
        forbid_descendants: ['*/**/bundle.yaml']
  - id: no-catch-all-bundle-names
    why: Name a distribution for its purpose; do not collect unrelated definitions.
    select:
      kind: directory
      include: [res/workflows, res/models]
    tree:
      forbid: [all, misc, common]
```

`tree.require` tests immediate children with the stated types. `tree.allow` is closed when present: unlisted immediate files or directories fail; omitted categories are empty allowances. Without `allow`, additional children are allowed. `forbid` checks immediate child names of either type and takes precedence over allowances; an exact require/forbid conflict is a configuration error. These immediate-child lists accept component globs only, not `/` or `**`.

`child_directories` applies to every immediate directory, even one also reported as disallowed. `min` counts them; `require_files` tests their immediate files. `forbid_descendants` uses path globs relative to each child. Here `*/**/bundle.yaml` catches manifests at any depth below a leaf's own manifest. A leaf may contain `assets/`, support directories, and several related contributions; “leaf” means no nested bundle, not no subdirectories. Symlinks never satisfy file/directory requirements.

An accepted tree is:

```text
res/
  README.md
  AGENTS.md
  workflows/
    README.md                    # optional group documentation
    review/
      bundle.yaml
      README.md
      review.star
      assets/prompt.md
  models/
    issue-triage/
      bundle.yaml
      README.md
      model.json
```

A loose `res/workflows/review.star`, a group `res/models/bundle.yaml`, or a child lacking a manifest fails. `res/README.md` and `res/AGENTS.md` are explicitly permitted and required; the same names are permitted, but optional, inside each group. Additional groups must be added deliberately. Deeper grouping such as `res/agents/workflows/<bundle>` uses another explicit grouping rule; do not infer arbitrary recursive groups by discovering manifests.

This enforces one directory and one root manifest per declared distribution, and prevents nested bundles and explicitly forbidden catch-all names. It cannot detect a catch-all renamed to `useful-things`. If a domain requires exactly one primary contribution per bundle, its owning library supplies a manifest-policy check with a declared primary kind and cardinality. Do not globally restrict bundles to one contribution: related support definitions and shared-library bundles are legitimate. Independence means an explicit distribution boundary with declared dependencies, not zero dependencies. Run the existing consumer `bundle.Load` test over the intended source set as well; tree checks neither create manifests nor replace that test.

### Example: README structure and metadata

```yaml
version: 1
rules:
  - id: bundle-readme
    why: Explain the distribution's purpose, use, and current implementation status.
    select:
      kind: file
      include: [res/workflows/*/README.md, res/models/*/README.md]
    markdown:
      frontmatter:
        required: true
        schema:
          type: object
          required: [title, owner, status]
          additionalProperties: false
          properties:
            title: {type: string, minLength: 1}
            owner: {type: string, minLength: 1}
            status: {enum: [draft, ready, experimental]}
      headings:
        - {text: Purpose, level: 2, count: 1, body: nonempty}
        - {text: Usage, level: 2, count: 1, body: nonempty}
        - {text: Implementation status, level: 2, count: 1, body: nonempty}
      order: [Purpose, Usage, Implementation status]
```

Parse Markdown as a document, not with line regexes: headings inside code fences do not count. Recognize ATX and setext headings through a pinned CommonMark parser. Match the case-sensitive rendered inline text after trimming and collapsing whitespace; formatting markers are not part of the text. Duplicate matching headings fail `count: 1`. `order` refers to uniquely required heading texts and imposes relative order without banning other sections.

A section extends to the next heading of equal or lower level. `body: nonempty` requires a non-heading, non-comment block containing non-whitespace content in that range; a code block counts. It does not establish usefulness or completeness. Frontmatter is one strict YAML object bounded by `---` lines at the start (optional UTF-8 BOM allowed); a missing closing delimiter fails. Its positions refer to original README lines. Frontmatter values use the same schema rules as structured files. An ordinary bold `Owner:` paragraph is prose, not a metadata field. Repositories that prefer no frontmatter can omit that check and require headings instead.

A file selector alone cannot prove that every bundle has a README: the tree rule supplies that guarantee. For example, the rule above accepts a README beginning with `title: Review workflow`, `owner: developer-tools`, and `status: ready` in frontmatter, followed by the three populated H2 sections.

### Example: Go import boundaries

```yaml
version: 1
rules:
  - id: contracts-stay-small
    why: Contracts must not depend on application implementations or third-party libraries.
    imports:
      module: src
      packages: ['./contracts/...', './features/...', './adapters/...']
      from: ['./contracts/...']
      forbid: ['./features/...', './adapters/...', external]
      allow: []
```

`imports` has no filesystem `select`. `module` is a literal repository-relative directory containing `go.mod`; `.` is permitted. `from`, `except`, `forbid`, `allow`, and optional `packages` map to the existing `archtest` concepts. `id` maps to `Name`, `why` to `Why`. `packages` inventories all non-test packages that must be accounted for in that Go module; it is not proof that each package has a restrictive rule. Multiple rules for a module must agree on the inventory, and the module is scanned once.

Import patterns retain Go syntax: `./contracts/...` includes the package and descendants; `.` names the module's root package; full import paths name external targets; `std` and `external` work only as import targets. All local patterns, including exceptions and allowances, must match an existing package. External targets may be absent: a ban on a dependency should pass before it is introduced. There is no filesystem `*` syntax here and no global optional-match escape for imports.

Go imports packages, not another `.go` file. A file-specific restriction, if later needed, would select source files and prohibit imported package paths using the same parser; it would not claim to model a file-to-file dependency. MVP inherits non-test-file and all-build-tag scanning from `archtest`, including its skip rules, rather than pretending to represent one build's dependency graph. Nested Go modules need explicit checks; root `go test ./...` does not run their tests. Reject `go.mod` replacements by default, as `archtest` does today.

### Example: structured JSON or YAML

```yaml
version: 1
rules:
  - id: profile-shape
    why: Every profile must declare its identity and a positive attempt limit.
    select:
      kind: file
      include: [config/profiles/*.yaml, config/profiles/*.json]
    structured:
      format: auto
      schema:
        type: object
        required: [id, limits]
        additionalProperties: false
        properties:
          id: {type: string, minLength: 1}
          limits:
            type: object
            required: [attempts]
            additionalProperties: false
            properties:
              attempts: {type: integer, minimum: 1}
```

This example intentionally requires at least one file of each extension. Use one extension, separate optional selectors, or an explicitly justified `on_empty: allow` when that is not the intended policy. `format: auto` accepts `.json`, `.yaml`, and `.yml` only; explicit `json` or `yaml` ignores the extension. Empty files and multiple/trailing documents fail. Reject duplicate JSON keys too; ordinary `encoding/json` decoding alone is insufficient. Preserve numbers without floating-point coercion.

Use JSON Schema draft 2020-12 by default, consistent with `kind/jsonschema`. Accept inline `schema` or a mutually exclusive literal `schema_file` relative to the repository root. Compile schemas once. File schemas are JSON, confined to the root; no remote or cross-file `$ref` loading, matching the existing extension's restriction. Internal references are supported. Required fields, types, enums, cardinalities, and forbidden fields use schema vocabulary rather than a parallel field-check language. Do not duplicate bundle manifest validation with generic schemas; `bundle.Load` remains authoritative.

## Evaluation and diagnostics

Proposed stages: strictly decode and validate configuration; enumerate eligible paths; resolve selectors; parse each needed file once; evaluate independent checks; sort and report every discoverable diagnostic. Independent files and rules continue after failures. A syntax error suppresses schema assertions for that file; unreadable directories report incomplete coverage, not an empty successful selection. Cancellation and exhausted resource limits fail the run and identify unchecked scope. Never convert skipped work into a pass.

Each diagnostic contains stable code, rule ID, repository-relative target path, optional 1-based line/column, optional JSON Pointer, configuration path/position, message, and the rule's `why`. Sort by target path, line, column, rule ID, code, then message; deduplicate only identical diagnostics. For missing files use the expected path and no invented source position. Syntax errors use exact token positions; schema errors map instance pointers to the nearest available source node. If an engine exposes no structured location, report a file or containing-node position explicitly rather than search for a matching field name and pretend it is exact.

Illustrative output (positions are examples):

```text
repository rules: 4 violations
config/profiles/default.yaml:5:15 [profile-shape] repo.schema_invalid /limits/attempts: expected integer >= 1, got 0
  why: Every profile must declare its identity and a positive attempt limit.
res/workflows/bundle.yaml [content-group-children] repo.entry_forbidden: grouping directory must not contain bundle.yaml
  why: Each child directory is one independently loadable distribution.
res/workflows/review/README.md [bundle-readme] repo.heading_missing: required H2 "Usage" is absent
  why: Explain the distribution's purpose, use, and current implementation status.
src/contracts/model.go:8:2 [contracts-stay-small] repo.import_forbidden: imports example.org/app/features/review
  why: Contracts must not depend on application implementations or third-party libraries.
```

The import position is a proposed enhancement; today's `archtest` records a filename, not this full position. Human output also includes `rule defined at archtest.yaml:<line>:<column>` per diagnostic. Machine output preserves the same fields and stable codes; text wording is not the compatibility surface. No warning-only mode, suppression comments, or generated baseline in MVP.

## Test integration and package ownership

The preferred MVP extends `archtest`, which already turns architectural rules into test failures. The following is a **proposed API, not implemented today**:

```go
package repository_test

import (
    "testing"

    "github.com/craggy-io/foundation-kit/archtest"
)

func TestRepositoryRules(t *testing.T) {
    archtest.CheckFile(t.Context(), t, "../archtest.yaml")
}
```

This test lives directly in `src/`, beside `src/go.mod`; `go test ./...` from `src` runs it. In a repository with a root Go module, use `"archtest.yaml"`. The argument is relative to the test package's working directory; the file's parent is the repository root for **all YAML paths**, including `imports.module`, selectors, exclusions, and schema files. Do not infer the root from the nearest `go.mod`, because `src/go.mod` and the repository root differ. No recursive discovery or rules folder is needed. Missing configuration fails. Additional nested Go modules must have their tests run explicitly; the root policy can inspect their imports without pretending their tests have run.

`CheckFile` calls `t.Helper`, reports all violations, and fails when evaluation is incomplete. Keep today's Go-configured `archtest.Check` useful for existing callers, sharing exactly the same import evaluator. YAML is the ordinary policy interface, not an optional future adapter. Separate tests still call `bundle.Load` or `bundletest.Load` with consumer-owned kinds, compilers, dependencies, and providers. YAML must never dynamically load Go compilers or start services.

The [agent guide](../../AGENTS.md), [`archtest/doc.go`](../../archtest/doc.go), and [`archtest/kit_test.go`](../../archtest/kit_test.go) constrain implementation:

1. `archtest` is already test support, permitted by the layer table to import any layer and `testing`, forbidden to production code. Its current implementation and documentation are standard-library-only; the layer rules do **not** impose that restriction on all test support. Adding YAML/Markdown/schema support changes its dependency footprint and must be documented and approved as a package decision. Prefer that explicit tradeoff over a second architecture-rule system. Keep parser types private; neither `kind` nor `bundle` imports `archtest`.
2. Refactor the existing private evaluator to return structured diagnostics shared by `Check` and `CheckFile`. Share scanning, matching, validation, positions, and ordering; do not scrape errors or reparse imports. No public evaluator or sibling package is needed for MVP.
3. `bundle/internal/strictyaml` is inaccessible to `archtest` under Go's internal-package rules. Propose moving its implementation once to module `internal/strictyaml`, updating every importer, test, architecture rule, and owning document, and deleting the old location. This needs an ADR defining ownership and permitted imports: the current rule that `internal/` inherits its parent's layer does not settle a module-root shared package. Do not copy the decoder or pretend this proposal has enacted the move.
4. Reuse `kind/jsonschema.New` from test support; an engine could not import that extension under current rules. Hide the Markdown parser in `archtest/internal/markdown`; assess dependency size before adoption. If preserving a standard-library-only import-check package becomes an explicit requirement, reconsider a narrowly scoped YAML extension calling the same evaluator. That is a fallback tradeoff, not today's recommendation. Heavy optional technology belongs in its own adapter Go module with separate checks.
5. Before implementation, record the cross-package decision in an ADR, add new architectural terms to ADR-0001 only if needed, and update package tables, root README, package README/status, `doc.go`, and layer tests. This proposal changes none of those contracts itself.

An optional CLI is later scope. Importing `archtest` from a production command would violate the test-support rule. A CLI requires a separately approved extraction of evaluation into appropriately layered non-test code, with the same evaluator used by tests. A future read-only `check` command could offer text/JSON output and distinguish pass (0), violations (1), and configuration/incomplete execution (2). MVP needs no CLI to fail normal tests.

## Portability, bounded work, and extension policy

Normalize diagnostic paths to `/`; never depend on drive letters, current working-directory discovery, modification times, locale, map order, or OS directory enumeration order. Require exact name casing and detect case-fold collisions even on case-sensitive hosts. Treat CRLF and LF equivalently for Markdown structure and positions. Validate UTF-8 input.

Do not follow symlinks, Windows junctions, or other directory redirections in checked scope. Report them as unsupported entries unless explicitly excluded before traversal; dangling links cannot satisfy requirements. Resolve and confine the supplied root and schema/config paths, and verify opened files remain regular in-root files. An ordinary working checkout is not an atomic snapshot: detect observed replacement/type changes and fail; use an immutable checkout for reproducible CI. This checker is not a security sandbox for concurrent hostile filesystem mutation.

Generic checks include hidden and generated files unless excluded explicitly. Import rules retain `archtest`'s documented scan policy, including generated `.go` files in eligible directories and skipped nested modules. Display these differing scopes in help/report metadata. Never silently reuse generic exclusions to weaken the import scanner.

Walk the generic tree once, index children and paths, parse only selected content, cache parsed documents by path within the run, and compile each schema once. Scan each Go module once through `archtest`; do not force its distinct policy into the generic walk. Avoid hashing bundle assets for structural checks. A separate `bundle.Load` test retains its existing cost and guarantees. Start sequentially; bounded parallel parsing is justified only by measurements and must preserve output order.

Initial proposed limits are 100,000 enumerated entries, 16 MiB per parsed text file, and 256 MiB total parsed text per run. Oversize binary assets need only enumeration, not parsing. Expose reviewed Go configuration overrides if real repositories need them; a limit breach is a failure with counters. Benchmark cold runs on a representative consumer and a large synthetic tree before setting a CI time budget; no performance result is claimed here.

Compose rules by conjunction: every applicable rule must pass, with allowances and exceptions local to their family. JSON Schema already supplies `allOf`, `anyOf`, and conditional checks for data; do not invent equivalents across unrelated files. MVP has no general `when`, expressions, scripting, network schema fetching, plugin discovery, or arbitrary callbacks selected by YAML. New built-in families require a concrete repeated need and fixture-defined semantics. Consumer-specific semantic checks stay ordinary Go tests; only introduce an explicit Go-registered extension interface after two independent consumers demonstrate a shared need.

## Delivery, tests, and acceptance criteria

MVP includes strict versioned config, deterministic diagnostics, the four families and syntax above, the Go-source restriction described above, and `archtest.CheckFile`. Schema-backed data and Markdown checks use established validators/parsers; implementation selection is settled before coding. Later work may include a CLI, source-file import filters, additional language analyzers, reusable policy files with pinned resolution, and domain-specific bundle cardinality helpers. None is required to enforce the example tree.

Implement and verify in this order:

1. Approve ownership/extraction ADR and precise configuration schema. Extract existing shared parsing/evaluation helpers at their narrowest legal common scope, preserving existing behavior with tests; remove replaced implementations completely.
2. Add config and selector fixtures, then directory rules, followed by the import adapter and structured/Markdown evaluators. Every introduced struct field must be traced through decoding, validation, execution, serialization, diagnostics, examples, and fixtures; unknown fields must never disappear silently.
3. Add self-tests with passing and one-fault fixtures for every example. Cover unmatched and partially unmatched includes, exclusions, stale exceptions, empty groups, wrong entry types, missing manifests/READMEs, loose files, nested manifests, allowed documentation, and forbidden catch-all names. Add forbidden authored/generated data in `src`, normal/blank/aliased/dot `embed` imports, indented and repeated directives, fake directives inside strings, and embedding behind build tags or in tests. Include a renamed catch-all as a documented semantic limitation.
4. Verify fenced fake headings, duplicate headings, frontmatter failures, empty sections, Unicode/CRLF, nested schema pointers, null versus absent fields, duplicate JSON/YAML keys, unknown config keys, disabled external references, cancellation, permission errors, limits, symlinks/junctions, and case collisions. Where platform permissions prevent link fixtures, report the skipped coverage explicitly and run them on a capable CI host.
5. Preserve `archtest` fixtures for unmatched patterns, package coverage, build-tag policy, test files, nested Go modules, replacements, and deterministic import reports. Add cross-family diagnostic ordering fixtures; repeated runs and relocated roots must produce identical normalized output. Run Windows and Linux checks.
6. Adopt one consumer with a normal test and existing runtime bundle-load tests. Convert import policies by comparing effective rules and violation fixtures, then remove the duplicate Go policy declarations in that consumer; retain `archtest.Check` for consumers that choose Go configuration. Do not change bundle discovery or loading during policy migration.

Acceptance requires that the accepted tree passes; each deliberate violation produces a stable rule/path/reason diagnostic; a misspelled selector or unknown configuration field fails; omitted README files cannot escape checking; all independent failures are reported together; normal tests exit unsuccessfully; and source files remain byte-for-byte unchanged. Existing `archtest` and bundle tests must continue to pass, including nested-module checks. Fixtures must prove that structurally valid but unloadable bundles still fail the separate runtime test. No claim of prose quality or conceptual bundle cohesion is part of acceptance.

## Decisions still required

- Confirm extending `archtest` with parser dependencies and the shared internal YAML ownership ADR. A public general-purpose evaluator would broaden the architectural work and is not needed just to enforce tests.
- Select the Markdown parser and approve its dependency cost; pin its interpretation with fixtures rather than assume all Markdown renderers agree.
- Decide whether source positions from JSON Schema need a small neutral diagnostic interface in the owning schema implementation. Do not export third-party error types or claim exact positions when unavailable.
- Confirm the initial glob subset and explicit fixed grouping depths. They keep failures predictable; recursive group inference is shorter to configure but lets accidental grouping directories escape scrutiny.
- Confirm consumer-specific exceptions for shared-library bundles and primary-contribution counts. Generic bundles can legitimately hold several contributions; their conceptual cohesion remains reviewable policy.
- Confirm measured limits and CI time budgets after a representative trial. This document proposes behavior and verification work; it contains no implemented feature or performance qualification.
