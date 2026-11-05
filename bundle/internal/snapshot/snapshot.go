// Package snapshot copies a bundle's files into an immutable in-memory view and
// enforces the path policy. Every later pipeline stage reads from the snapshot, so
// the bytes that are digested are the bytes that are compiled.
//
// Assets are the exception. Files under AssetsDir are checked and hashed as a stream,
// but their bytes aren't kept, and bundle.Asset verifies them when they're read.
package snapshot

import "io/fs"

// AssetsDir is the directory at the bundle root that holds assets.
const AssetsDir = "assets"

// Limits bounds what one bundle may contain. These are errors rather than truncation.
type Limits struct {
	MaxFiles      int   // counts every file, including assets
	MaxFileBytes  int64 // applies only to files held in memory, not to assets
	MaxTotalBytes int64 // applies only to files held in memory, not to assets
}

// AssetInfo is what a snapshot records about one asset instead of its bytes.
type AssetInfo struct {
	Size   int64
	Digest string
}

// Snapshot is an immutable copy of a bundle's accepted files, with assets recorded
// by size and digest only.
type Snapshot struct {
	files  map[string][]byte    // slash-separated relative path to exact bytes
	assets map[string]AssetInfo // slash-separated relative path, under AssetsDir
}

// Files returns the snapshot's contents, for hashing. Callers must not modify it.
func (s *Snapshot) Files() map[string][]byte { return s.files }

// Assets returns what was recorded about each file under AssetsDir. Callers must not
// modify it.
func (s *Snapshot) Assets() map[string]AssetInfo { return s.assets }

// FS returns a read-only view of the snapshot, used as bundle.Contribution.Files. It
// doesn't contain assets.
//
// TODO: implement. Avoid testing/fstest here, because this is non-test code.
func (s *Snapshot) FS() fs.FS {
	panic("snapshot.FS: not implemented")
}

// Diagnostic is a snapshot problem. The bundle package converts it to a
// bundle.Diagnostic.
type Diagnostic struct {
	Code    string // one of the code strings declared in bundle/diagnostic.go
	Path    string
	Message string
}

// Take walks fsys, copies every accepted file outside AssetsDir, and hashes every
// file under it.
//
// TODO: implement.
//
//   - Reject symlinks and other non-regular files (fs.ReadLink via fs.ReadLinkFS, or
//     the entry's file mode) rather than following them, including under AssetsDir.
//   - Reject a bundle.yaml anywhere except the root, including under AssetsDir, so
//     that one directory can never be read as two bundles.
//   - Apply Limits while reading, not afterwards.
//   - Stream each asset through digest.New, and don't hold its bytes.
//   - Never reopen a snapshotted path after this returns. Only bundle.Asset reopens
//     files, and it checks them against the digest recorded here.
func Take(fsys fs.FS, limits Limits) (*Snapshot, []Diagnostic) {
	panic("snapshot.Take: not implemented")
}
