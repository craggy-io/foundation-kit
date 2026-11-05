package bundle

import (
	"fmt"
	"slices"
	"strings"

	"github.com/craggy-io/foundation-kit/kind"
)

// Definition is the validated, immutable value of one contribution or built-in.
type Definition[T any] struct {
	Ref   Ref
	Value T

	// Owner identifies the bundle or provider this definition came from.
	Owner Identity

	// Assets lists the assets its contribution declares in bundle.yaml, in the order
	// written there. Their bytes aren't loaded until something reads them. It is
	// empty for a built-in.
	Assets []Asset
}

// Catalog is the immutable result of a successful Load. It holds the definitions
// from bundles and from providers in one list, and is read through All and Get so
// that consumers receive typed values.
//
// To support reloading later, build a new Catalog with Load and swap the pointer.
// A Catalog is never changed in place. Its assets' bytes stay in their Source, so
// reading an Asset can fail if that Source has since changed.
type Catalog struct {
	digest      string
	owners      []Identity
	order       []Ref // every key of definitions, sorted
	definitions map[Ref]storedDefinition
	overrides   map[Ref]Ref // target to replacement, as applied by Load
}

// storedDefinition is a Definition with its type erased. Only Get reads the value.
type storedDefinition struct {
	kind   kind.ID
	owner  Identity
	value  any
	assets []Asset
}

// All returns every definition of kind k in c, sorted by Ref.
func All[T any](c *Catalog, k *kind.Kind[T]) []Definition[T] {
	var defs []Definition[T]
	for _, ref := range c.order {
		if def, ok := Get(c, k, ref); ok {
			defs = append(defs, def)
		}
	}
	return defs
}

// Get returns the definition at ref. It reports false if ref does not exist in c or
// belongs to a different kind.
func Get[T any](c *Catalog, k *kind.Kind[T], ref Ref) (Definition[T], bool) {
	stored, ok := c.definitions[ref]
	if !ok || stored.kind != k.ID() {
		return Definition[T]{}, false
	}
	value, ok := stored.value.(T)
	if !ok {
		return Definition[T]{}, false
	}
	return Definition[T]{
		Ref:    ref,
		Value:  value,
		Owner:  stored.owner,
		Assets: slices.Clone(stored.assets),
	}, true
}

// Digest returns the catalog closure digest: the identity of the selected bundles
// and providers, their definitions, dependency bindings and kinds taken together.
// Use it as configuration identity instead of walking bundle directories separately.
//
// It covers a bundle's exact bytes, but only a provider's ID and Version. Changing a
// built-in's value without changing its provider's Version does not change the digest.
func (c *Catalog) Digest() string { return c.digest }

// Owners returns the identity of every accepted bundle and provider, sorted by ID.
func (c *Catalog) Owners() []Identity {
	return append([]Identity(nil), c.owners...)
}

// Explain describes where ref came from: its kind, owner, origin and digest, its
// assets, and any Override that applied. It is intended for humans and diagnostic
// tools, and its format is not stable.
func (c *Catalog) Explain(ref Ref) string {
	def, ok := c.definitions[ref]
	if !ok {
		return fmt.Sprintf("%s: not in the catalog\n", ref)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", ref)
	fmt.Fprintf(&b, "  kind:    %s\n", def.kind)
	if replacement, ok := c.overrides[ref]; ok {
		fmt.Fprintf(&b, "  override: replaced by %s, so the rest describes that definition\n", replacement)
	}
	var replaces []string
	for target, replacement := range c.overrides {
		if replacement == ref {
			replaces = append(replaces, target.String())
		}
	}
	slices.Sort(replaces)
	for _, target := range replaces {
		fmt.Fprintf(&b, "  override: replaces %s\n", target)
	}
	fmt.Fprintf(&b, "  owner:   %s %s\n", def.owner.ID, def.owner.Version)
	fmt.Fprintf(&b, "  origin:  %s\n", def.owner.Origin)
	if def.owner.Digest == "" {
		fmt.Fprintf(&b, "  digest:  none, because built-ins have no file bytes\n")
	} else {
		fmt.Fprintf(&b, "  digest:  %s\n", def.owner.Digest)
	}
	for _, a := range def.assets {
		fmt.Fprintf(&b, "  asset:   %s (%d bytes, %s)\n", a.path, a.size, a.digest)
	}
	return b.String()
}
