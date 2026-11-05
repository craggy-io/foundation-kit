// Package link checks that a set of definitions forms a complete,
// unambiguous graph. It works on plain strings so that it stays free of type-erased
// values and can be tested on its own.
package link

import (
	"fmt"
	"sort"
	"strings"
)

// Codes reported by this package. They match the codes declared in
// bundle/diagnostic.go.
const (
	codeDuplicateID          = "bundle.duplicate_id"
	codeReferenceMissing     = "bundle.reference_missing"
	codeDependencyUndeclared = "bundle.dependency_undeclared"
	codeDependencyMissing    = "bundle.dependency_missing"
	codeDependencyMismatch   = "bundle.dependency_mismatch"
	codeOverrideInvalid      = "bundle.override_invalid"
)

// Owner is one bundle or provider.
type Owner struct {
	ID     string
	Digest string // a bundle's content digest; empty for a provider

	// Bundle is true for a bundle and false for a provider.
	Bundle bool

	// Dependencies lists the bundles a bundle's manifest declares. A provider has none.
	Dependencies []Dependency
}

// Dependency is one declared dependency, optionally pinned to a digest.
type Dependency struct {
	ID     string
	Digest string
}

// Node is one definition, from a bundle or a provider.
type Node struct {
	Ref   string   // "owner-id#id"
	Kind  string   // "namespace.name@N"
	Owner string   // bundle or provider ID
	Refs  []string // references the compiler reported; none for a built-in
}

// Override makes references to Target resolve to Replacement.
type Override struct {
	Target      string
	Replacement string
}

// Input is the candidate graph.
type Input struct {
	Owners    []Owner
	Nodes     []Node
	Overrides []Override
}

// Diagnostic is a link problem. The bundle package converts it to a
// bundle.Diagnostic.
type Diagnostic struct {
	Code    string // one of the code strings declared in bundle/diagnostic.go
	Owner   string // the bundle or provider the problem is in, if any
	Ref     string // the definition the problem is in, if any
	Message string
	Related []string
}

// Resolve checks the graph and returns each reference's final target, with
// overrides applied. The result has an entry for every reference a node reports and
// for every override target.
//
// Input order never picks a winner: duplicate owner IDs, duplicate references and a
// target overridden twice are all errors. A reference into another owner must be to
// a declared dependency, every declared dependency must be present, and a pinned
// dependency must have the pinned digest. An override's target and replacement must
// both exist and have the same kind, and a replacement can't itself be overridden,
// so that every binding takes exactly one step.
func Resolve(in Input) (map[string]string, []Diagnostic) {
	var diags []Diagnostic
	report := func(d Diagnostic) { diags = append(diags, d) }

	owners := map[string]Owner{}
	for _, o := range in.Owners {
		if _, dup := owners[o.ID]; dup {
			report(Diagnostic{Code: codeDuplicateID, Owner: o.ID,
				Message: fmt.Sprintf("more than one bundle or provider has the ID %q", o.ID)})
			continue
		}
		owners[o.ID] = o
	}

	nodes := map[string]Node{}
	for _, n := range in.Nodes {
		if _, dup := nodes[n.Ref]; dup {
			report(Diagnostic{Code: codeDuplicateID, Owner: n.Owner, Ref: n.Ref,
				Message: fmt.Sprintf("more than one definition has the reference %s", n.Ref)})
			continue
		}
		nodes[n.Ref] = n
	}

	// Dependencies must be present, and match their pins.
	for _, o := range in.Owners {
		for _, dep := range o.Dependencies {
			target, ok := owners[dep.ID]
			switch {
			case !ok:
				report(Diagnostic{Code: codeDependencyMissing, Owner: o.ID,
					Message: fmt.Sprintf("dependency %q is not among the loaded bundles", dep.ID)})
			case dep.Digest != "" && target.Digest != dep.Digest:
				got := target.Digest
				if got == "" {
					got = "no digest, because it is a provider"
				}
				report(Diagnostic{Code: codeDependencyMismatch, Owner: o.ID,
					Message: fmt.Sprintf("dependency %q is pinned to %s, but has %s", dep.ID, dep.Digest, got)})
			}
		}
	}

	// Overrides: each target at most once, both ends present with the same kind.
	// A target named by two overrides is reported for both, so that neither wins.
	targetCount := map[string]int{}
	for _, o := range in.Overrides {
		targetCount[o.Target]++
	}
	overrides := map[string]string{}
	for _, o := range in.Overrides {
		target, hasTarget := nodes[o.Target]
		replacement, hasReplacement := nodes[o.Replacement]
		var problem string
		switch {
		case o.Target == o.Replacement:
			problem = "a definition can't replace itself"
		case targetCount[o.Target] > 1:
			problem = fmt.Sprintf("%s is the target of %d overrides", o.Target, targetCount[o.Target])
		case !hasTarget:
			problem = "the target doesn't exist"
		case !hasReplacement:
			problem = "the replacement doesn't exist"
		case target.Kind != replacement.Kind:
			problem = fmt.Sprintf("the target has kind %s but the replacement has kind %s", target.Kind, replacement.Kind)
		case targetCount[o.Replacement] > 0:
			problem = "the replacement is itself overridden, and overrides don't chain"
		}
		if problem != "" {
			report(Diagnostic{Code: codeOverrideInvalid, Owner: target.Owner, Ref: o.Target,
				Message: fmt.Sprintf("override of %s with %s: %s", o.Target, o.Replacement, problem),
				Related: []string{o.Replacement}})
			continue
		}
		overrides[o.Target] = o.Replacement
	}

	bindings := map[string]string{}
	for target, replacement := range overrides {
		bindings[target] = replacement
	}
	for _, n := range in.Nodes {
		declared := map[string]bool{}
		for _, d := range owners[n.Owner].Dependencies {
			declared[d.ID] = true
		}
		for _, ref := range n.Refs {
			owner, _, ok := strings.Cut(ref, "#")
			if _, exists := nodes[ref]; !ok || !exists {
				report(Diagnostic{Code: codeReferenceMissing, Owner: n.Owner, Ref: n.Ref,
					Message: fmt.Sprintf("%s refers to %s, which doesn't exist", n.Ref, ref),
					Related: []string{ref}})
				continue
			}
			if owner != n.Owner && !declared[owner] {
				report(Diagnostic{Code: codeDependencyUndeclared, Owner: n.Owner, Ref: n.Ref,
					Message: fmt.Sprintf("%s refers to %s, but %s doesn't declare %q in dependencies", n.Ref, ref, n.Owner, owner),
					Related: []string{ref}})
				continue
			}
			final := ref
			if r, ok := overrides[ref]; ok {
				final = r
			}
			bindings[ref] = final
		}
	}

	if len(diags) > 0 {
		sort.SliceStable(diags, func(i, j int) bool {
			a, b := diags[i], diags[j]
			if a.Owner != b.Owner {
				return a.Owner < b.Owner
			}
			if a.Ref != b.Ref {
				return a.Ref < b.Ref
			}
			if a.Code != b.Code {
				return a.Code < b.Code
			}
			return a.Message < b.Message
		})
		return nil, diags
	}
	return bindings, nil
}
