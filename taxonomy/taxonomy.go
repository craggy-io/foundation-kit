package taxonomy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// Type is one named type and the types it is a kind of.
type Type struct {
	// Name is "namespace.name", or a single label for a root or a namespace.
	Name string `json:"name"`
	// Parents are the types this one is a kind of. A type with none is a root.
	Parents     []string `json:"parents,omitempty"`
	Description string   `json:"description,omitempty"`
	// Abstract types group others and are never the most specific answer
	// while a concrete descendant holds.
	Abstract bool `json:"abstract,omitempty"`
}

// normalize trims the name and parents, and drops duplicate, empty and
// self-referential parents. Parents come back sorted.
func (t Type) normalize() Type {
	t.Name = strings.TrimSpace(t.Name)
	t.Description = strings.TrimSpace(t.Description)
	var parents []string
	for _, p := range t.Parents {
		p = strings.TrimSpace(p)
		if p != "" && p != t.Name && !slices.Contains(parents, p) {
			parents = append(parents, p)
		}
	}
	slices.Sort(parents)
	t.Parents = parents
	return t
}

func (t Type) clone() Type {
	t.Parents = slices.Clone(t.Parents)
	return t
}

// fingerprint length-prefixes every variable field, so two different types
// can't collide through a value that contains the separator.
func (t Type) fingerprint() string {
	var b strings.Builder
	field := func(s string) { fmt.Fprintf(&b, "%d:%s|", len(s), s) }
	field(t.Name)
	field(t.Description)
	fmt.Fprintf(&b, "parents%d|", len(t.Parents))
	for _, p := range t.Parents {
		field(p)
	}
	fmt.Fprintf(&b, "%t", t.Abstract)
	return b.String()
}

// Set is the types one owner declares.
type Set struct {
	// Owner identifies who declared the types: a library, a bundle or a host.
	Owner string
	Types []Type
}

// Namespace returns the part of name before the first dot, or "" for a
// single-label name.
func Namespace(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return ""
}

// ValidName reports whether name is a well-formed type name: dot-separated,
// non-empty segments of lower-case letters, digits and "-_+".
func ValidName(name string) bool {
	if name == "" || name[0] == '.' || name[len(name)-1] == '.' || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '+':
		default:
			return false
		}
	}
	return true
}

// Taxonomy is a validated, immutable graph of types. It is safe for
// concurrent use. The zero value is not usable; get one from New or a Live.
type Taxonomy struct {
	types       map[string]Type
	owners      map[string][]string        // type -> owners that declared it, sorted
	ancestors   map[string]map[string]bool // excludes self
	descendants map[string]map[string]bool // excludes self
	depth       map[string]int             // longest path to a root
	names       []string                   // sorted
	revision    string
}

// New validates sets into a taxonomy. Two owners may declare the same type
// when their declarations are identical; a differing one is a conflict. New
// reports every problem it finds, as an *Error.
func New(sets ...Set) (*Taxonomy, error) {
	t := &Taxonomy{
		types:       map[string]Type{},
		owners:      map[string][]string{},
		ancestors:   map[string]map[string]bool{},
		descendants: map[string]map[string]bool{},
		depth:       map[string]int{},
	}
	var diags []Diagnostic
	report := func(code Code, typ, owner, format string, args ...any) {
		diags = append(diags, Diagnostic{Code: code, Type: typ, Owner: owner, Message: fmt.Sprintf(format, args...)})
	}

	seenOwner := map[string]bool{}
	for _, s := range sets {
		owner := strings.TrimSpace(s.Owner)
		if owner == "" || seenOwner[owner] {
			report(CodeOwnerInvalid, "", owner, "owner %q is empty or listed twice", owner)
			continue
		}
		seenOwner[owner] = true
		for _, ty := range s.Types {
			ty = ty.normalize()
			if !ValidName(ty.Name) {
				report(CodeNameInvalid, ty.Name, owner, "invalid type name %q (want lower-case namespace.name)", ty.Name)
				continue
			}
			if prev, ok := t.types[ty.Name]; ok {
				if prev.fingerprint() != ty.fingerprint() {
					report(CodeTypeConflict, ty.Name, owner, "type %q redeclared differently by %s (first declared by %s)", ty.Name, owner, t.owners[ty.Name][0])
					continue
				}
			} else {
				t.types[ty.Name] = ty
			}
			if !slices.Contains(t.owners[ty.Name], owner) {
				t.owners[ty.Name] = append(t.owners[ty.Name], owner)
			}
		}
	}
	for name := range t.types {
		t.names = append(t.names, name)
		slices.Sort(t.owners[name])
	}
	slices.Sort(t.names)

	for _, name := range t.names {
		for _, p := range t.types[name].Parents {
			if _, ok := t.types[p]; !ok {
				report(CodeParentUnknown, name, t.owners[name][0], "type %q has unknown parent %q", name, p)
			}
		}
	}

	const (
		white = iota
		grey
		black
	)
	colour := map[string]int{}
	cyclic := map[string]bool{}
	var visit func(string) bool // false when name is on, or reaches, a cycle
	visit = func(name string) bool {
		switch colour[name] {
		case grey:
			if !cyclic[name] {
				cyclic[name] = true
				report(CodeCycle, name, t.owners[name][0], "cycle in type graph at %q", name)
			}
			return false
		case black:
			return !cyclic[name]
		}
		colour[name] = grey
		anc := map[string]bool{}
		depth, ok := 0, true
		for _, p := range t.types[name].Parents {
			if _, declared := t.types[p]; !declared {
				continue // already reported
			}
			if !visit(p) {
				ok = false
				continue
			}
			anc[p] = true
			for a := range t.ancestors[p] {
				anc[a] = true
			}
			depth = max(depth, t.depth[p]+1)
		}
		if !ok {
			cyclic[name] = true
		}
		t.ancestors[name] = anc
		t.depth[name] = depth
		colour[name] = black
		return ok
	}
	for _, name := range t.names {
		visit(name)
	}

	for _, name := range t.names {
		ns := Namespace(name)
		if _, declared := t.types[ns]; declared && !cyclic[name] && !t.ancestors[name][ns] {
			report(CodeNamespaceEscape, name, t.owners[name][0], "type %q is in namespace %q but does not descend from it", name, ns)
		}
	}
	if len(diags) > 0 {
		return nil, &Error{Diagnostics: diags}
	}

	h := sha256.New()
	for _, name := range t.names {
		fmt.Fprintln(h, t.types[name].fingerprint())
		for a := range t.ancestors[name] {
			if t.descendants[a] == nil {
				t.descendants[a] = map[string]bool{}
			}
			t.descendants[a][name] = true
		}
	}
	t.revision = hex.EncodeToString(h.Sum(nil))[:16]
	return t, nil
}

// Revision identifies the taxonomy's content: its types, not who owns them.
func (t *Taxonomy) Revision() string { return t.revision }

// Has reports whether name is a declared type.
func (t *Taxonomy) Has(name string) bool {
	_, ok := t.types[name]
	return ok
}

// Get returns a copy of one type.
func (t *Taxonomy) Get(name string) (Type, bool) {
	ty, ok := t.types[name]
	return ty.clone(), ok
}

// Owners returns the owners that declared name, sorted.
func (t *Taxonomy) Owners(name string) []string { return slices.Clone(t.owners[name]) }

// IsA reports whether child is parent or one of its descendants. An
// undeclared type is not anything, not even itself.
func (t *Taxonomy) IsA(child, parent string) bool {
	if child == parent {
		return t.Has(child)
	}
	return t.ancestors[child][parent]
}

// Ancestors returns every type name is a kind of, excluding itself, sorted.
func (t *Taxonomy) Ancestors(name string) []string { return sortedKeys(t.ancestors[name]) }

// Descendants returns every type that is a kind of name, excluding itself,
// sorted.
func (t *Taxonomy) Descendants(name string) []string { return sortedKeys(t.descendants[name]) }

// Depth is the length of the longest parent path from name to a root. Deeper
// types are more specific.
func (t *Taxonomy) Depth(name string) int { return t.depth[name] }

// Closure returns the given types plus all their ancestors.
func (t *Taxonomy) Closure(types map[string]bool) map[string]bool {
	out := make(map[string]bool, len(types)*2)
	for name := range types {
		out[name] = true
		for a := range t.ancestors[name] {
			out[a] = true
		}
	}
	return out
}

// Roots returns the types with no parents, sorted.
func (t *Taxonomy) Roots() []string {
	var out []string
	for _, name := range t.names {
		if len(t.types[name].Parents) == 0 {
			out = append(out, name)
		}
	}
	return out
}

// Types returns every type, sorted by name.
func (t *Taxonomy) Types() []Type {
	out := make([]Type, len(t.names))
	for i, name := range t.names {
		out[i] = t.types[name].clone()
	}
	return out
}

// Len returns the number of types.
func (t *Taxonomy) Len() int { return len(t.names) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
