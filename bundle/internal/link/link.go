// Package link checks that a set of definitions forms a complete,
// unambiguous graph. It works on plain strings so that it stays free of type-erased
// values and can be tested on its own.
package link

import (
	"fmt"
	"slices"
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
	codeIDConflict           = "bundle.id_conflict"
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

	// OwnerOverrides are bundle-level overrides: Target and Replacement are owner
	// IDs, not references. Each expands into one Override per contribution ID that
	// the replacement chain shares with the target.
	OwnerOverrides []Override

	// Global lists the kinds ("namespace.name@N") whose effective definitions must
	// have catalog-wide unique contribution IDs.
	Global map[string]bool
}

// Result is a successful resolution.
type Result struct {
	// Bindings maps each reference a node reports, and each override target, to its
	// final target.
	Bindings map[string]string

	// Overrides maps each applied override target to its replacement, including
	// those expanded from OwnerOverrides.
	Overrides map[string]string
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
// overrides applied. The result's Bindings has an entry for every reference a node
// reports and for every override target.
//
// Input order never picks a winner: duplicate owner IDs, duplicate references and a
// target overridden twice are all errors. A reference into another owner must be to
// a declared dependency, every declared dependency must be present, and a pinned
// dependency must have the pinned digest. An override's target and replacement must
// both exist and have the same kind, and a replacement can't itself be overridden,
// so that every binding takes exactly one step.
//
// OwnerOverrides are expanded first (see expandOwnerOverrides), and the overrides
// they produce are checked exactly like the explicit ones. Finally, every effective
// definition of a Global kind (one that isn't an override target) must have a
// contribution ID that no other effective definition of that kind has.
func Resolve(in Input) (Result, []Diagnostic) {
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

	expanded, ownerDiags := expandOwnerOverrides(in.OwnerOverrides, owners, in.Nodes)
	diags = append(diags, ownerDiags...)
	all := append(append([]Override(nil), in.Overrides...), expanded...)

	// Overrides: each target at most once, both ends present with the same kind.
	// A target named by two overrides is reported for both, so that neither wins.
	targetCount := map[string]int{}
	for _, o := range all {
		targetCount[o.Target]++
	}
	overrides := map[string]string{}
	for _, o := range all {
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

	diags = append(diags, checkGlobal(in.Global, in.Nodes, overrides)...)

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
		return Result{}, diags
	}
	return Result{Bindings: bindings, Overrides: overrides}, nil
}

// expandOwnerOverrides turns bundle-level overrides into reference overrides.
//
// Each owner may be the target of at most one bundle-level override, so the
// overrides form chains, such as shipped replaced by user replaced by project. For
// every contribution x of a target T, the replacement is T's highest replacement in
// the chain that also has x, so every override it produces takes one step: in that
// chain, shipped#x and user#x both resolve to project#x when all three have x, and
// shipped#x resolves to user#x when project lacks it. A contribution that only a
// replacement has is simply added.
//
// Unknown owners, self-overrides, owners targeted twice and cycles are reported, and
// nothing is expanded from an invalid set, because its chains are undefined.
func expandOwnerOverrides(in []Override, owners map[string]Owner, nodes []Node) ([]Override, []Diagnostic) {
	if len(in) == 0 {
		return nil, nil
	}
	var diags []Diagnostic
	report := func(o Override, problem string) {
		diags = append(diags, Diagnostic{Code: codeOverrideInvalid, Owner: o.Target,
			Message: fmt.Sprintf("bundle override of %q with %q: %s", o.Target, o.Replacement, problem),
			Related: []string{o.Replacement}})
	}
	targetCount := map[string]int{}
	for _, o := range in {
		targetCount[o.Target]++
	}
	next := map[string]string{}
	for _, o := range in {
		_, hasTarget := owners[o.Target]
		_, hasReplacement := owners[o.Replacement]
		switch {
		case o.Target == o.Replacement:
			report(o, "an owner can't replace itself")
		case !hasTarget:
			report(o, "the target is not among the loaded bundles and providers")
		case !hasReplacement:
			report(o, "the replacement is not among the loaded bundles and providers")
		case targetCount[o.Target] > 1:
			report(o, fmt.Sprintf("%q is the target of %d bundle overrides", o.Target, targetCount[o.Target]))
		default:
			next[o.Target] = o.Replacement
		}
	}

	// Cycles: walk each chain, and report each cycle once, at its smallest owner.
	reported := map[string]bool{}
	for start := range next {
		seen := map[string]bool{}
		for at := start; at != ""; at = next[at] {
			if !seen[at] {
				seen[at] = true
				continue
			}
			var cycle []string
			for c := at; ; {
				cycle = append(cycle, c)
				if c = next[c]; c == at {
					break
				}
			}
			sort.Strings(cycle)
			if !reported[cycle[0]] {
				reported[cycle[0]] = true
				diags = append(diags, Diagnostic{Code: codeOverrideInvalid, Owner: cycle[0],
					Message: fmt.Sprintf("bundle overrides form a cycle: %s", strings.Join(cycle, ", ")),
					Related: cycle[1:]})
			}
			break
		}
	}
	if len(diags) > 0 {
		return nil, diags
	}

	ids := map[string]map[string]bool{} // owner to its contribution IDs
	for _, n := range nodes {
		_, id, _ := strings.Cut(n.Ref, "#")
		if ids[n.Owner] == nil {
			ids[n.Owner] = map[string]bool{}
		}
		ids[n.Owner][id] = true
	}
	var out []Override
	for _, n := range nodes {
		if _, ok := next[n.Owner]; !ok {
			continue
		}
		_, id, _ := strings.Cut(n.Ref, "#")
		final := ""
		for r := next[n.Owner]; r != ""; r = next[r] {
			if ids[r][id] {
				final = r + "#" + id
			}
		}
		if final != "" {
			out = append(out, Override{Target: n.Ref, Replacement: final})
		}
	}
	return out, nil
}

// checkGlobal reports effective definitions of a global kind that share a
// contribution ID across owners. An override target isn't effective, because its
// replacement stands in for it.
func checkGlobal(global map[string]bool, list []Node, overrides map[string]string) []Diagnostic {
	if len(global) == 0 {
		return nil
	}
	type key struct{ kind, id string }
	groups := map[key][]string{}
	for _, n := range list {
		if !global[n.Kind] {
			continue
		}
		if _, overridden := overrides[n.Ref]; overridden {
			continue
		}
		_, id, _ := strings.Cut(n.Ref, "#")
		k := key{n.Kind, id}
		if !slices.Contains(groups[k], n.Ref) {
			groups[k] = append(groups[k], n.Ref)
		}
	}
	var diags []Diagnostic
	for k, refs := range groups {
		if len(refs) < 2 {
			continue
		}
		sort.Strings(refs)
		for _, ref := range refs {
			var others []string
			for _, o := range refs {
				if o != ref {
					others = append(others, o)
				}
			}
			owner, _, _ := strings.Cut(ref, "#")
			diags = append(diags, Diagnostic{Code: codeIDConflict, Owner: owner, Ref: ref,
				Message: fmt.Sprintf("kind %s needs catalog-wide unique IDs, but %q is also defined by %s; "+
					"rename one, or replace one with an override", k.kind, k.id, strings.Join(others, ", ")),
				Related: others})
		}
	}
	return diags
}
