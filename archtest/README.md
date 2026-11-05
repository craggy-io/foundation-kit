# archtest

`archtest` turns a repository's architecture rules into a test failure, so that a boundary violation fails `go test` instead of waiting for a review to catch it.

```go
import "github.com/craggy-io/foundation-kit/archtest"
```

This is test support, so only test files may import it. It uses only the standard library.

## Writing rules

Put a test anywhere in your Go module and list the rules as Go values:

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

`Check` reports every problem at once, and each failure names the file and the reason:

```text
archtest: 1 problem(s):
  example.com/app/features/chat imports database/sql (store.go)
    rule "only the data feature uses SQL": other features call the data feature's API
```

## Patterns

| Pattern | Matches |
| --- | --- |
| `./kind` | That package only |
| `./bundle/...` | That package and everything below it |
| `.` | The root package of the Go module |
| `net/http`, `github.com/x/y/...` | A full import path, or everything below it |
| `archtest.Std` | The standard library, in `Forbid` and `Allow` only |
| `archtest.External` | Any other Go module, in `Forbid` and `Allow` only |

`From` and `Except` can only name packages in your Go module. A bare path in `Forbid`, such as `kind`, means a standard library package rather than `./kind`.

## What else `Check` fails on

- **`replace` directives:** `go.mod` must have none. Use an uncommitted `go.work` instead, or set `AllowReplace` if your repository has a reason to keep one.
- **Rules that can't work:** a rule with no `Why`, or a local pattern that matches no package, fails. Otherwise a misspelled path would pass forever.
- **Uncovered packages:** if you set `Packages`, a package it doesn't match fails, so a new package has to be placed deliberately. Packages with only test files are exempt.
- **Unreadable files:** a Go file whose imports can't be parsed is reported with the other problems.

## What it doesn't check

- **Test files:** these may reach across layers to assemble fixtures.
- **Nested Go modules:** each one has its own `go.mod` and needs its own `Check`.
- **Non-import rules:** it can't tell whether business logic is in the right package, or whether a file is too long.
- **Your dependencies' imports:** it looks only at this Go module's source.

Build constraints are ignored, so an import behind `//go:build plan9` counts on every platform. That applies to `//go:build ignore` files too.

## Implementation status

Already working and tested:

- `Check`, `Config`, `Rule`, `Std` and `External`
- This kit's own layer rules, in [`kit_test.go`](kit_test.go)

Still to build, when `cclab-platform` adopts it:

1. [ ] A check that dependencies don't import the consumer back. `cclab-platform`'s `TestLibrariesDoNotImportPlatform` does this, and it needs the go command to find the dependencies' source.
