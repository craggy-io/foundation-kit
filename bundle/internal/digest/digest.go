// Package digest computes content identities. The encoding here is effectively a
// storage format, because digests are persisted by consumers. Changing it changes
// every identity, so it needs golden tests from the first implementation.
package digest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"slices"
)

// New returns a hash for computing the digest of one file as a stream, so that a
// large file doesn't need to be held in memory. Read the result with Sum.
func New() hash.Hash { return sha256.New() }

// Sum returns h's digest in the form "sha256:<hex>". It doesn't change h.
func Sum(h hash.Hash) string { return "sha256:" + hex.EncodeToString(h.Sum(nil)) }

// Bundle returns the content digest of one bundle. It covers the sorted relative
// paths and exact bytes of files, which is the snapshot including bundle.yaml, and
// the sorted relative paths and digests of assets, whose bytes aren't in memory.
//
// The encoding is a version label followed by two sections, files then assets.
// Each section is its entry count followed by its entries in bytewise path order,
// and every string in it is length-prefixed, so different file sets cannot produce
// the same input, and moving a file into or out of the assets directory always
// changes the digest. Modification times, permissions and absolute locations are
// not part of it, and bytes are not normalized, so Git line-ending conversion
// changes the digest: repositories holding bundles need a .gitattributes rule that
// keeps their files byte-identical on every platform.
func Bundle(files map[string][]byte, assets map[string]string) string {
	e := newEncoder("craggy.io/bundle-digest/v1")
	e.section(len(files))
	for _, p := range sortedKeys(files) {
		e.str(p)
		e.bytes(files[p])
	}
	e.stringMap(assets)
	return Sum(e.h)
}

// CatalogInput is everything that determines a catalog's identity.
type CatalogInput struct {
	BundleDigests map[string]string // bundle ID to content digest
	Providers     map[string]string // provider ID to version; a built-in has no bytes to hash
	Contributions []string          // references of every definition
	Bindings      map[string]string // resolved reference and override targets
	Kinds         []string          // "namespace.name@N" of the kinds in use
}

// Catalog returns the closure digest of a whole catalog, using the same
// length-delimited encoding as Bundle. Contributions and Kinds are sorted first, so
// their order doesn't matter.
func Catalog(in CatalogInput) string {
	e := newEncoder("craggy.io/catalog-digest/v1")
	e.stringMap(in.BundleDigests)
	e.stringMap(in.Providers)
	e.stringList(in.Contributions)
	e.stringMap(in.Bindings)
	e.stringList(in.Kinds)
	return Sum(e.h)
}

type encoder struct{ h hash.Hash }

func newEncoder(label string) *encoder {
	e := &encoder{h: New()}
	e.str(label)
	return e
}

func (e *encoder) uint(n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	e.h.Write(b[:])
}

func (e *encoder) section(n int) { e.uint(uint64(n)) }
func (e *encoder) str(s string)  { e.uint(uint64(len(s))); e.h.Write([]byte(s)) }
func (e *encoder) bytes(b []byte) {
	e.uint(uint64(len(b)))
	e.h.Write(b)
}

func (e *encoder) stringMap(m map[string]string) {
	e.section(len(m))
	for _, k := range sortedKeys(m) {
		e.str(k)
		e.str(m[k])
	}
}

func (e *encoder) stringList(list []string) {
	sorted := slices.Clone(list)
	slices.Sort(sorted)
	e.section(len(sorted))
	for _, s := range sorted {
		e.str(s)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
