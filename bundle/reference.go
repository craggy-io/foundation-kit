package bundle

import (
	"fmt"
	"strings"
)

// Ref is the qualified name of one definition, written "owner-id#id". The owner is
// the bundle or provider it came from. It is comparable, so it can be used as a map
// key.
type Ref struct {
	Owner string
	ID    string
}

// String returns the "owner-id#id" form.
func (r Ref) String() string { return r.Owner + "#" + r.ID }

// ParseRef parses the "owner-id#id" form.
func ParseRef(s string) (Ref, error) {
	owner, id, ok := strings.Cut(s, "#")
	if !ok || owner == "" || id == "" || strings.Contains(id, "#") {
		return Ref{}, fmt.Errorf("bundle: invalid reference %q: want owner-id#id", s)
	}
	return Ref{Owner: owner, ID: id}, nil
}

// Identity describes one accepted bundle or provider.
type Identity struct {
	// ID is metadata.id from bundle.yaml or kind.Provider.ID, unique within a Catalog.
	ID string

	// Version is the release its author issued. It is a label and proves nothing
	// about content. Digest does that.
	Version string

	// Digest covers the sorted relative paths and exact bytes of a bundle's accepted
	// files, including bundle.yaml and its assets. It is empty for a provider, whose
	// built-ins have no file bytes, which records that their content identity is
	// unknown.
	Digest string

	// Origin is Source.Origin for a bundle, or "builtin" for a provider. It is not
	// part of Digest.
	Origin string
}
