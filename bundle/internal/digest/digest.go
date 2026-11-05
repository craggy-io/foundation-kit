// Package digest computes content identities. The encoding here is effectively a
// storage format, because digests are persisted by consumers. Changing it changes
// every identity, so it needs golden tests from the first implementation.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
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
// TODO: implement with SHA-256 and a "sha256:<hex>" result.
//
//   - Sort paths bytewise and length-delimit each path and body, so that different
//     file sets cannot produce the same input.
//   - Keep files and assets distinct in the encoding, so that moving a file into or
//     out of the assets directory always changes the digest.
//   - Exclude modification times, permissions and absolute locations.
//   - Do not normalize bytes. This means Git line-ending conversion changes the
//     digest, so repositories holding bundles need a .gitattributes rule that keeps
//     their files byte-identical on every platform.
func Bundle(files map[string][]byte, assets map[string]string) string {
	panic("digest.Bundle: not implemented")
}

// CatalogInput is everything that determines a catalog's identity.
type CatalogInput struct {
	BundleDigests map[string]string // bundle ID to content digest
	Providers     map[string]string // provider ID to version; a built-in has no bytes to hash
	Contributions []string          // sorted references
	Bindings      map[string]string // resolved reference and override targets
	Kinds         []string          // sorted "namespace.name@N" of the kinds in use
}

// Catalog returns the closure digest of a whole catalog.
//
// TODO: implement with the same length-delimited encoding as Bundle.
func Catalog(in CatalogInput) string {
	panic("digest.Catalog: not implemented")
}
