// Package archtest turns a repository's architecture rules into a test failure, so a
// boundary violation fails go test instead of waiting for someone to notice it.
//
// A test calls Check with a list of rules. Each rule says which packages it applies
// to, which imports they must not have, and why. Check parses the import declarations
// of every non-test Go file in the Go module and reports every violation at once.
//
// Test files are exempt, because they may reach across layers to assemble fixtures.
// A nested Go module is skipped, because it has its own go.mod and needs its own
// Check. Files are parsed whatever their build constraints, so an import behind a
// constraint still counts.
//
// Check also fails on a rule that can't do its job, such as one whose pattern matches
// no package, because a rule with a misspelled path otherwise passes forever.
//
// This package imports only the standard library and testing. It holds no state, and
// is safe to use from parallel tests.
//
// # Exported names
//
//	Check           run the rules and fail the test on any problem
//	Config          what to check
//	Rule            one forbidden set of imports, and the reason for it
//	Std, External   patterns for the standard library and for other Go modules
package archtest
