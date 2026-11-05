package kind

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// namePattern requires a namespace, so that kinds from different libraries
	// cannot collide: lower-case dot-separated segments, at least two of them.
	namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

	// versionPattern allows one spelling per version, so "01" is rejected.
	versionPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// ID names a kind and the schema version used to interpret it. It is written as
// "namespace.name@N", for example "cclab.intel-pack@1".
type ID struct {
	Name    string
	Version int
}

// ParseID parses and validates the "namespace.name@N" form. A successful result's
// String method returns exactly s.
func ParseID(s string) (ID, error) {
	name, version, ok := strings.Cut(s, "@")
	if !ok {
		return ID{}, fmt.Errorf("kind: invalid kind %q: want namespace.name@N", s)
	}
	if !versionPattern.MatchString(version) {
		return ID{}, fmt.Errorf("kind: invalid kind %q: version must be 1 or higher, with no leading zeros", s)
	}
	n, err := strconv.Atoi(version)
	if err != nil {
		return ID{}, fmt.Errorf("kind: invalid kind %q: version out of range", s)
	}
	id := ID{Name: name, Version: n}
	if err := id.Validate(); err != nil {
		return ID{}, err
	}
	return id, nil
}

// Validate reports whether id can be written as "namespace.name@N".
func (id ID) Validate() error {
	if !namePattern.MatchString(id.Name) {
		return fmt.Errorf("kind: invalid kind name %q: want lower-case namespace.name", id.Name)
	}
	if id.Version < 1 {
		return fmt.Errorf("kind: invalid version %d for kind %q: must be 1 or higher", id.Version, id.Name)
	}
	return nil
}

// String returns the "namespace.name@N" form.
func (id ID) String() string {
	return fmt.Sprintf("%s@%d", id.Name, id.Version)
}
