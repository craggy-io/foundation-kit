// Package link checks that a set of definitions forms a complete,
// unambiguous graph. It works on plain strings so that it stays free of type-erased
// values and can be tested on its own.
package link

// Node is one definition, from a bundle or a provider.
type Node struct {
	Ref   string   // "owner-id#id"
	Kind  string   // "namespace.name@N"
	Owner string   // bundle or provider ID
	Refs  []string // references the compiler reported; none for a built-in
}

// Input is the candidate graph.
type Input struct {
	Nodes        []Node
	Dependencies map[string][]string // bundle ID to the bundle IDs it declares
	Overrides    map[string]string   // target reference to replacement reference
}

// Diagnostic is a link problem. The bundle package converts it to a
// bundle.Diagnostic.
type Diagnostic struct {
	Code    string // one of the code strings declared in bundle/diagnostic.go
	Ref     string
	Message string
	Related []string
}

// Resolve checks the graph and returns each reference's final target, with
// overrides applied.
//
// TODO: implement.
//
//   - Reject duplicate owner IDs and references. Input order must never pick a winner.
//   - Report each reference that does not resolve.
//   - Report a reference into another bundle unless that bundle is declared as a
//     dependency of the referencing bundle.
//   - Check that each override's target and replacement exist and have the same kind.
//   - Report dependency digest pin mismatches. This needs the accepted digests added
//     to Input.
func Resolve(in Input) (map[string]string, []Diagnostic) {
	panic("link.Resolve: not implemented")
}
