package bundle

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/craggy-io/foundation-kit/kind"
)

// Metadata is the bundle-wide presentation and optional trigger declaration.
// The host interpreting Trigger decides its meaning; bundle only preserves it.
type Metadata struct {
	Title       string
	Description string
	Tags        []string
	Trigger     json.RawMessage
}

func (m Metadata) clone() Metadata {
	m.Tags = slices.Clone(m.Tags)
	m.Trigger = slices.Clone(m.Trigger)
	return m
}

// Definition is the validated, immutable value of one contribution or built-in.
type Definition[T any] struct {
	Ref      Ref
	Value    T
	Metadata Metadata

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
// A Catalog is never changed in place: Live reloads by building a new one and
// swapping the pointer. Its assets' bytes stay in their Source, so reading an Asset
// can fail if that Source has since changed.
type Catalog struct {
	digest      string
	owners      []Identity
	order       []Ref // every key of definitions, sorted
	definitions map[Ref]storedDefinition
	overrides   map[Ref]Ref                // target to replacement, as applied by Load
	explicit    map[Ref]bool               // targets named by Options.Overrides
	layers      map[string]string          // Options.BundleOverrides, target owner to replacement owner
	global      map[kind.ID]map[string]Ref // effective definitions of global kinds, by contribution ID
	compiled    map[compileKey]compiledValue
	files       map[string]fs.FS // each bundle's snapshot, by owner ID
}

// storedDefinition is a Definition with its type erased. Only Get reads the value.
type storedDefinition struct {
	kind     kind.ID
	owner    Identity
	value    any
	assets   []Asset
	metadata Metadata
}

// All returns every effective definition of kind k in c, sorted by Ref. Each
// appears once, under its own Ref: an override target is left out, because its
// replacement is listed instead. Use Get to read a target through its override.
func All[T any](c *Catalog, k *kind.Kind[T]) []Definition[T] {
	var defs []Definition[T]
	for _, ref := range c.order {
		if _, overridden := c.overrides[ref]; overridden {
			continue
		}
		if def, ok := Get(c, k, ref); ok {
			defs = append(defs, def)
		}
	}
	return defs
}

// Get returns the definition at ref. It reports false if ref does not exist in c or
// belongs to a different kind. If ref is an override target, Get returns the
// replacement's value, owner and assets, with Ref still set to ref.
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
		Ref:      ref,
		Value:    value,
		Owner:    stored.owner,
		Assets:   slices.Clone(stored.assets),
		Metadata: stored.metadata.clone(),
	}, true
}

// Lookup returns the effective definition of a global kind (see kind.Global) whose
// contribution ID is id, whichever bundle or provider owns it. Load guarantees there
// is at most one. The result's Ref is the definition's own Ref, so after an override
// it names the replacement.
//
// It reports false if no effective definition has that ID, and always reports false
// for a kind declared without kind.Global, whose IDs aren't unique across owners:
// read those with Get or All instead.
func Lookup[T any](c *Catalog, k *kind.Kind[T], id string) (Definition[T], bool) {
	if !k.Global() {
		return Definition[T]{}, false
	}
	ref, ok := c.global[k.ID()][id]
	if !ok {
		return Definition[T]{}, false
	}
	return Get(c, k, ref)
}

// Overrides returns every replacement Load applied, from Options.Overrides and from
// expanding Options.BundleOverrides, sorted by Target. A consumer can use it to
// report which definitions are shadowed.
func (c *Catalog) Overrides() []Override {
	out := make([]Override, 0, len(c.overrides))
	for target, replacement := range c.overrides {
		out = append(out, Override{Target: target, Replacement: replacement})
	}
	slices.SortFunc(out, func(a, b Override) int { return compareRefs(a.Target, b.Target) })
	return out
}

// Digest returns the catalog closure digest: the identity of the selected bundles
// and providers, their definitions, dependency bindings and kinds taken together.
// Use it as configuration identity instead of walking bundle directories separately.
//
// It covers a bundle's exact bytes, but only a provider's ID and Version. Changing a
// built-in's value without changing its provider's Version does not change the digest.
func (c *Catalog) Digest() string { return c.digest }

// Files returns the accepted files of the bundle with the given owner ID: its
// manifest, contributions and support files, exactly as Load read and hashed
// them. Assets aren't included, so read those through a definition's Assets. It
// reports false for a provider, which has no files, and for an unknown owner.
//
// Use it for work that needs a bundle's files after Load has returned, such as
// building a program from source a bundle ships. Reading the bundle's folder
// instead would give whatever is on disk now, which may no longer match the
// owner's Digest. The view is read-only, and holds no ignored files.
func (c *Catalog) Files(owner string) (fs.FS, bool) {
	files, ok := c.files[owner]
	return files, ok
}

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
		fmt.Fprintf(&b, "  override: replaced by %s%s, so the rest describes that definition\n", replacement, c.via(ref))
	}
	var replaces []string
	for target, replacement := range c.overrides {
		if replacement == ref {
			replaces = append(replaces, target.String())
		}
	}
	slices.Sort(replaces)
	for _, target := range replaces {
		t, _ := ParseRef(target)
		fmt.Fprintf(&b, "  override: replaces %s%s\n", target, c.via(t))
	}
	fmt.Fprintf(&b, "  owner:   %s %s\n", def.owner.ID, def.owner.Version)
	if def.metadata.Title != "" {
		fmt.Fprintf(&b, "  title:   %s\n", def.metadata.Title)
	}
	if def.metadata.Description != "" {
		fmt.Fprintf(&b, "  description: %s\n", def.metadata.Description)
	}
	if len(def.metadata.Tags) > 0 {
		fmt.Fprintf(&b, "  tags:    %s\n", strings.Join(def.metadata.Tags, ", "))
	}
	if len(def.metadata.Trigger) > 0 {
		fmt.Fprintf(&b, "  trigger: %s\n", def.metadata.Trigger)
	}
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

// via says where the override of target came from, for Explain.
func (c *Catalog) via(target Ref) string {
	if c.explicit[target] {
		return ""
	}
	if replacement, ok := c.layers[target.Owner]; ok {
		return fmt.Sprintf(" (bundle override of %s by %s)", target.Owner, replacement)
	}
	return ""
}
