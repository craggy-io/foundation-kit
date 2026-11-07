# ADR-0003: Repository rules in archtest, and a shared internal package

**Status:** Proposed
**Date:** 2026-11-07
**Deciders:** philcantcode

## Context

[`docs/proposals/repository-rules.md`](../proposals/repository-rules.md) asks for a repository's structural rules (where files go, what a README contains, what shape a JSON or YAML file has, which packages may import which) to be written in one reviewed YAML file and enforced by the normal test command. It recommends extending `archtest` rather than adding a second checker, and leaves these decisions open:

1. Whether `archtest` may take parser dependencies. Until now it imported only the standard library.
2. How `archtest` shares the strict YAML decoder. It lived in `bundle/internal/strictyaml`, which Go's internal-package rule hides from `archtest`, and the layer rule that `internal/` belongs to its parent says nothing about an `internal/` at the module root.
3. Which Markdown parser to use.
4. How a JSON Schema failure is positioned in the source text without exposing the schema library's types.

## Decision

### `archtest` owns repository rules

`archtest.CheckFile(ctx, t, path)` reads an `archtest.yaml` and fails the test on every violation. It supports five rule families: `tree` (directory layout), `go_source` (forbidden imports and directives in selected Go files), `markdown` (frontmatter and headings), `structured` (JSON and YAML against a JSON Schema) and `imports`. An `imports` rule runs through exactly the same evaluator as `archtest.Check`, which stays for consumers that prefer Go configuration. There is no second import parser and no public evaluator.

`archtest` is test support, so it may import any layer. It now imports:

- `internal/strictyaml`, for `archtest.yaml`, YAML data files and frontmatter.
- `kind/jsonschema`, an extension, to compile and run schemas.
- `github.com/yuin/goldmark` v1.7.8, a CommonMark parser with no dependencies of its own, hidden in `archtest/internal/markdown`. No goldmark type leaves that package.

A consumer that imports `archtest` therefore compiles these dependencies in its tests, but never in production code, because only tests may import test support.

### Module-root `internal/` is a shared leaf

`internal/` at the module root holds private code that packages in more than one layer need. `bundle/internal/strictyaml` moves there, with no copy left behind.

- It may import the standard library and small third-party dependencies, and **no other kit package** except another package under `internal/`.
- Engines, extensions and test support may import it. Contracts may not, because they import only the standard library.
- An importer must not expose its types in an exported API, just as an engine hides a third-party dependency.
- `archtest/kit_test.go` enforces the first rule as "shared internal code is a leaf".

The rule that `internal/` belongs to its parent still applies to every other `internal/` directory.

### Positions for schema failures

`kind/jsonschema` gains `Problems(err) ([]Problem, bool)`, which splits a validation error into a JSON Pointer and an English message per failing value. `archtest` maps each pointer to the nearest value its own parser recorded a position for. Validation errors themselves are unchanged, so `bundle` diagnostics are unaffected.

### Semantics settled during implementation

- **Globs** support `*`, `?` and whole-component `**`, which matches zero or more components, so `res/**` matches `res` itself. Paths are case-sensitive everywhere.
- **Grouping depth** is explicit: each grouping directory is named in a selector, and manifests are never discovered to infer groups.
- **Exceptions** are literal paths. One that suppresses nothing in a run is reported as stale, so a fixed problem can't leave its exception behind.
- **Imports** reject `go.mod` replacements unless every rule for the module sets `allow_replace: true`.
- **Limits** are 100,000 entries, 16 MiB per parsed file and 256 MiB per run. Exceeding one fails the run. They aren't configurable yet; add a reviewed Go option when a real repository needs it.
- **No machine-readable output** yet. Diagnostics have stable codes (`repo.*`), but the only output is the test failure, because there is no CLI.

## Options considered

### A sibling package or a separate checker

Rejected, as in the proposal: two rule systems would drift, and `archtest` already turns rules into test failures.

### Keep `archtest` standard-library-only, and add a YAML extension module

Rejected for now. It would need the same evaluator exported across modules, and the dependencies only reach test binaries anyway. Revisit it if a consumer needs the import checks without the parsers.

### Copy the strict YAML decoder into `archtest`

Rejected. Two decoders would accept different YAML over time.

### Export the schema library's error type

Rejected. It would make a third-party type part of the kit's API, which the layer rules forbid for engines and which would be no better for an extension.

## Consequences

- **Easier:** a consumer describes layout, README structure, data shapes and import boundaries in one file, and `go test` fails with a positioned, explained diagnostic for each violation.
- **Harder:** `archtest` now has dependencies to keep up to date, and goldmark's interpretation of CommonMark is pinned by fixtures in `archtest/internal/markdown`.
- **Limits:** rules check structure, not meaning: a renamed catch-all bundle, inaccurate prose or a bundle that doesn't load all pass. Consumers still need their own `bundle.Load` tests.
