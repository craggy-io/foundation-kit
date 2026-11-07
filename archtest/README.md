# archtest

`archtest` turns a repository's rules into test failures, so that a misplaced file, a missing README section or a boundary violation fails `go test` instead of waiting for a review to catch it.

```go
import "github.com/craggy-io/foundation-kit/archtest"
```

This is test support, so only test files may import it. It has two entry points that share one import evaluator:

- **`CheckFile`** reads rules from an `archtest.yaml`: directory layout, Go source restrictions, Markdown structure, JSON and YAML shapes, and import boundaries. Use this by default, because changing a policy needs no Go.
- **`Check`** takes import rules as Go values.

It depends on [`internal/strictyaml`](../internal/strictyaml), [`kind/jsonschema`](../kind/jsonschema) and the goldmark Markdown parser, as decided in [ADR-0003](../docs/decisions/ADR-0003-repository-rules-and-shared-internal.md). They only reach test binaries.

## Rules in archtest.yaml

Put `archtest.yaml` at the repository root and call it from a test:

```go
func TestRepositoryRules(t *testing.T) {
	archtest.CheckFile(t.Context(), t, "../archtest.yaml")
}
```

The path is relative to the test's working directory, and **the file's directory is the root that every path inside it is relative to**. So a test beside `src/go.mod` passes `"../archtest.yaml"`, and a test in a root Go module passes `"archtest.yaml"`. A missing or invalid file fails the test.

```yaml
version: 1
exclude:                       # optional; pruned before any rule runs
  - {path: build/**, why: Build output is not authored content.}
rules:
  - id: source-files-only      # unique; names the rule in failures
    why: Authored content belongs under root res.   # required: what to do instead
    select:
      kind: directory          # file or directory
      include: [src, 'src/**']
      exclude: []              # optional
      on_empty: error          # default; "allow" needs empty_reason
    tree:
      allow:
        files: ['*.go', go.mod, go.sum, README.md]
        directories: ['*']
      forbid: [res, assets, data, '*.db']
    exceptions:                # optional; literal paths only
      - {path: src/legacy.db, why: Removed when the importer ships.}
```

Each rule has an `id`, a `why` and exactly one check: `tree`, `go_source`, `markdown`, `structured` or `imports`. Unknown fields, wrong types, duplicate keys or IDs, YAML anchors, aliases, tags and multiple documents are all errors, and an invalid file checks nothing rather than passing part of the way. A complete example of each family is in [`repo_test.go`](repo_test.go).

### Paths and selectors

- Paths use `/`, are relative to the root, and are case-sensitive on every OS. `.` is the root. Absolute paths, backslashes, `..` and a leading `./` are rejected.
- Globs support `*` (within one name), `?` (one character) and `**` (zero or more whole directories, so `res/**/README.md` includes `res/README.md` and `res/**` includes `res`). Nothing else is special.
- **Each `include` must match** at least one entry of the selected kind, after `exclude`, so a misspelled path fails instead of checking nothing. Set `on_empty: allow` with an `empty_reason` for a scope that is genuinely optional. A file never matches a directory selector, or the reverse.
- Root `exclude` removes paths from every rule except `imports`. Only `.git` is excluded implicitly; `.gitignore` is not read. Pointing a selector, exception or required child at an excluded path is a `repo.config_conflict`.

### `tree`: directory layout

Checks each selected directory's immediate children. Lists hold single-name globs.

| Field | Meaning |
| --- | --- |
| `require.files`, `require.directories` | Each pattern must match at least one child of that type |
| `allow.files`, `allow.directories` | If `allow` is present, any other child fails. It asserts nothing exists. |
| `forbid` | Children of either type that must not exist. Wins over `allow`. |
| `child_directories.min` | At least this many child directories |
| `child_directories.require_files` | Files each child directory must contain |
| `child_directories.forbid_descendants` | Path globs, relative to each child directory, that must match nothing below it |

For example, `forbid_descendants: ['*/**/bundle.yaml']` allows a leaf bundle's own `bundle.yaml` but no nested one.

### `go_source`: Go source restrictions

Checks exactly the selected `.go` files, including tests, generated files, nested Go modules and files behind any build constraint.

- `forbid_imports` lists import paths (or `path/...`). Blank, renamed and dot imports all count.
- `forbid_directives` lists directive names such as `go:embed`. Only real `//name` line comments count, indented or misplaced ones included. Strings, block comments and `// go:embed` don't.

### `markdown`: README structure

```yaml
markdown:
  frontmatter:                 # optional
    required: true             # default
    schema: {type: object, required: [title, owner]}   # or schema_file
  headings:
    - {text: Purpose, level: 2, count: 1, body: nonempty}
  order: [Purpose, Usage]
```

Headings come from a CommonMark parse, so `#` lines in code fences don't count, and only top-level headings start sections. Text is matched exactly after formatting is removed and whitespace collapsed. `count` defaults to 1 and is exact. `body: nonempty` needs a non-heading block with text before the next heading of the same or a higher level; a code block counts and an HTML comment doesn't. `order` names headings with count 1. Frontmatter is a strict YAML mapping between `---` lines at the start of the file. CRLF and a byte order mark are accepted.

A file selector can't prove a README exists; pair it with a `tree` rule that requires one.

### `structured`: JSON and YAML shapes

```yaml
structured:
  format: auto                 # auto (by .json, .yaml, .yml), json or yaml
  schema_file: schemas/profile.json   # or an inline schema
```

Schemas are JSON Schema (draft 2020-12 unless `$schema` says otherwise), compiled once, with no remote or cross-file `$ref`. Files must hold exactly one value. Duplicate keys fail in JSON as well as YAML, and numbers are kept exact. A schema failure reports the JSON Pointer of the failing value and its line and column.

### `imports`: Go import boundaries

```yaml
imports:
  module: src                  # directory holding go.mod, relative to the root
  packages: ['./...']          # optional inventory, as Config.Packages
  from: ['./contracts/...']
  except: []
  forbid: ['./features/...', external]
  allow: []
  allow_replace: false         # default
```

These map onto `Rule` and `Config` and use the same patterns as `Check`, described below. Every rule for one module runs in a single scan, and they must agree on `packages` and `allow_replace`. Imports keep `Check`'s scan policy (non-test files, every build constraint, skipping `testdata`, `vendor`, hidden directories and nested Go modules), whatever the root `exclude` says.

### Failures

Every problem is reported at once, sorted by path and position:

```text
archtest: archtest.yaml: 2 problem(s):
  config/profiles/default.yaml:3:13 [profile-shape] repo.schema_invalid /limits/attempts: minimum: got 0, want 1
    why: Every profile must declare its identity and a positive attempt limit.
    rule defined at archtest.yaml:3:5
  res/models/bundle.yaml [content-group-children] repo.entry_forbidden: file "bundle.yaml" is forbidden here
    why: Each child directory is one independently loadable distribution.
    rule defined at archtest.yaml:19:5
```

Match on the `repo.*` code, not the message. A missing file is reported at its expected path with no position. Excluded paths and the exceptions that suppressed something are logged, so `go test -v` shows them.

### Things that fail the run

- **Stale configuration:** an include that matches nothing, or an exception that suppressed nothing.
- **Special entries:** symlinks, junctions and other non-regular entries aren't followed and fail unless excluded. They never satisfy a requirement.
- **Case collisions:** two names in one directory that differ only in case.
- **Incomplete checks:** an unreadable directory or file, a file replaced during the run, cancellation, or a limit: 100,000 entries, 16 MiB per parsed file, 256 MiB parsed per run. Skipped work is never a pass.

## Import rules in Go

`Check` takes the same import rules as Go values:

```go
func TestArchitecture(t *testing.T) {
	archtest.Check(t, archtest.Config{
		Packages: []string{"./cmd/...", "./features/...", "./internal/..."},
		Rules: []archtest.Rule{{
			Name:   "only the data feature uses SQL",
			Why:    "other features call the data feature's API",
			From:   []string{"./features/..."},
			Except: []string{"./features/data"},
			Forbid: []string{"database/sql"},
		}},
	})
}
```

```text
archtest: 1 problem(s):
  features/chat/store.go:5:2 [only the data feature uses SQL] repo.import_forbidden: example.com/app/features/chat imports database/sql
    why: other features call the data feature's API
```

### Patterns

| Pattern | Matches |
| --- | --- |
| `./kind` | That package only |
| `./bundle/...` | That package and everything below it |
| `.` | The root package of the Go module |
| `net/http`, `github.com/x/y/...` | A full import path, or everything below it |
| `archtest.Std` (`std`) | The standard library, in `Forbid` and `Allow` only |
| `archtest.External` (`external`) | Any other Go module, in `Forbid` and `Allow` only |

`From` and `Except` can only name packages in your Go module. A bare path in `Forbid`, such as `kind`, means a standard library package rather than `./kind`.

### What else `Check` fails on

- **`replace` directives:** `go.mod` must have none. Use an uncommitted `go.work` instead, or set `AllowReplace` if your repository has a reason to keep one.
- **Rules that can't work:** a rule with no `Why`, or a local pattern that matches no package, fails. Otherwise a misspelled path would pass forever.
- **Uncovered packages:** if you set `Packages`, a package it doesn't match fails, so a new package has to be placed deliberately. Packages with only test files are exempt.
- **Unreadable files:** a Go file whose imports can't be parsed is reported with the other problems.

## What it doesn't check

- **Meaning:** it can require a heading, a nonempty section or a bundle boundary, but not that the prose is right or that a bundle's contents belong together. A catch-all renamed to `useful-things` passes.
- **Loading:** a bundle laid out correctly may still fail `bundle.Load`. Keep a `bundle.Load` or `bundletest.Load` test over your content.
- **Runtime behaviour:** a rule against `go:embed` doesn't prove all content is loaded through `bundle`, and a layout rule sees only files present when the test runs.
- **Test files and nested modules, for imports:** test files may reach across layers to assemble fixtures, and each nested Go module needs its own rule or `Check`. Root `go test ./...` doesn't run a nested module's tests.
- **Your dependencies' imports:** it looks only at this repository's source.

Import scans ignore build constraints, so an import behind `//go:build plan9` counts on every platform. That applies to `//go:build ignore` files too.

## Implementation status

Already working and tested:

- `Check`, `Config`, `Rule`, `Std` and `External`, with positioned diagnostics
- `CheckFile` and `archtest.yaml` version 1: strict decoding, selectors, globs, root exclusions, exceptions, and the `tree`, `go_source`, `markdown`, `structured` and `imports` families
- Stable `repo.*` codes, deterministic ordering, limits, cancellation, and rejection of symlinks, junctions and case collisions
- This kit's own layer rules, in [`kit_test.go`](kit_test.go), and repository rules, in the root [`archtest.yaml`](../archtest.yaml)

Not tested on Windows, where these tests skip: case collisions and unreadable directories. Run them on Linux CI.

Still to build:

1. [ ] A check that dependencies don't import the consumer back, when `cclab-platform` adopts this. Its `TestLibrariesDoNotImportPlatform` does this, and it needs the go command to find the dependencies' source.
2. [ ] Configurable limits, once a real repository needs different ones.
3. [ ] Machine-readable output, if a CLI is approved. It would need evaluation moved out of test support.
