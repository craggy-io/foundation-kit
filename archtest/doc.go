// Package archtest turns a repository's rules into test failures, so a misplaced
// file or a boundary violation fails go test instead of waiting for someone to
// notice it.
//
// CheckFile reads rules from an archtest.yaml whose directory is the repository
// root. Each rule has an id, a why and one check: tree (which files and directories
// a directory may, must or must not hold), go_source (imports and directives that
// selected Go files must not have), markdown (frontmatter and headings), structured
// (JSON and YAML files against a JSON Schema) or imports. Check takes import rules as
// Go values instead. Both report every problem at once, each with a stable repo.*
// code, a position where there is one, and the rule's why.
//
// Import rules, from either entry point, run through one evaluator. It parses the
// import declarations of every non-test Go file in a Go module, whatever their build
// constraints, and skips testdata, vendor, hidden directories and nested Go modules,
// which need their own rules.
//
// A rule that can't do its job fails rather than passing forever: an unknown field, a
// selector or import pattern that matches nothing, or an exception that suppresses
// nothing. Checking never stops at the first problem, never follows links, and never
// turns work it couldn't do, such as an unreadable directory or a limit reached, into
// a pass.
//
// The package holds no state and is safe to use from parallel tests. It depends on
// internal/strictyaml, kind/jsonschema and, through internal/markdown, the goldmark
// Markdown parser (ADR-0003).
//
// # Exported names
//
//	Check           run Go import rules and fail the test on any problem
//	CheckFile       run the rules in an archtest.yaml and fail the test on any problem
//	Config          what Check examines
//	Rule            one forbidden set of imports, and the reason for it
//	Std, External   patterns for the standard library and for other Go modules
package archtest
