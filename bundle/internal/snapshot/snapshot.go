// Package snapshot copies a bundle's files into an immutable in-memory view and
// enforces the path policy. Every later pipeline stage reads from the snapshot, so
// the bytes that are digested are the bytes that are compiled.
//
// Assets are the exception. Files under AssetsDir are checked and hashed as a stream,
// but their bytes aren't kept, and bundle.Asset verifies them when they're read.
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/craggy-io/foundation-kit/bundle/internal/digest"
)

// AssetsDir is the directory at the bundle root that holds assets.
const AssetsDir = "assets"

// manifestName is the manifest's file name, which may only appear at the root.
const manifestName = "bundle.yaml"

// Codes reported by this package. They match the codes declared in
// bundle/diagnostic.go.
const (
	codeManifestNested   = "bundle.manifest_nested"
	codePathForbidden    = "bundle.path_forbidden"
	codeSymlinkForbidden = "bundle.symlink_forbidden"
	codeLimitExceeded    = "bundle.limit_exceeded"
	codeReadFailed       = "bundle.source_unavailable"
)

// Limits bounds what one bundle may contain. These are errors rather than truncation.
// A zero field means no limit.
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

// Diagnostic is a snapshot problem. The bundle package converts it to a
// bundle.Diagnostic.
type Diagnostic struct {
	Code    string // one of the code strings declared in bundle/diagnostic.go
	Path    string
	Message string
}

// Take walks fsys, copies every accepted file outside AssetsDir, and hashes every
// file under it. It reports every problem it finds, and returns a nil Snapshot if
// there are any.
//
// Ignored files and directories (see Ignored) are left out as if they weren't
// there. Symlinks and other non-regular files are rejected rather than followed,
// and so is a bundle.yaml anywhere but the root. Limits are applied while reading. After Take
// returns, nothing reopens a snapshotted path except bundle.Asset, which checks
// what it reads against the digest recorded here.
func Take(ctx context.Context, fsys fs.FS, limits Limits) (*Snapshot, []Diagnostic) {
	fsys = Visible(fsys)
	t := &taker{ctx: ctx, fsys: fsys, limits: limits,
		snap: &Snapshot{files: map[string][]byte{}, assets: map[string]AssetInfo{}}}
	err := fs.WalkDir(fsys, ".", t.visit)
	if err != nil && !errors.Is(err, errStop) {
		t.report(codeReadFailed, "", err.Error())
	}
	if len(t.diags) > 0 {
		return nil, t.diags
	}
	return t.snap, nil
}

var errStop = errors.New("snapshot: stop")

type taker struct {
	ctx    context.Context
	fsys   fs.FS
	limits Limits
	snap   *Snapshot
	diags  []Diagnostic

	files int
	total int64
}

func (t *taker) report(code, p, msg string) {
	t.diags = append(t.diags, Diagnostic{Code: code, Path: p, Message: msg})
}

func (t *taker) visit(p string, d fs.DirEntry, err error) error {
	if ctxErr := t.ctx.Err(); ctxErr != nil {
		t.report(codeReadFailed, p, ctxErr.Error())
		return errStop
	}
	if err != nil {
		t.report(codeReadFailed, p, err.Error())
		if d != nil && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	}
	mode := d.Type()
	switch {
	case mode&fs.ModeSymlink != 0:
		t.report(codeSymlinkForbidden, p, fmt.Sprintf("%s is a symlink, which bundles may not contain", p))
		return nil
	case d.IsDir():
		return nil
	case !mode.IsRegular():
		t.report(codePathForbidden, p, fmt.Sprintf("%s is not a regular file", p))
		return nil
	}
	if path.Base(p) == manifestName && p != manifestName {
		t.report(codeManifestNested, p, fmt.Sprintf("%s: %s is only allowed at the bundle root", p, manifestName))
		return nil
	}
	if strings.ContainsAny(p, `\`) {
		t.report(codePathForbidden, p, fmt.Sprintf("%q contains a backslash", p))
		return nil
	}

	t.files++
	if t.limits.MaxFiles > 0 && t.files > t.limits.MaxFiles {
		t.report(codeLimitExceeded, p, fmt.Sprintf("the bundle has more than %d files", t.limits.MaxFiles))
		return errStop
	}
	if strings.HasPrefix(p, AssetsDir+"/") {
		t.hashAsset(p)
		return nil
	}
	return t.copyFile(p)
}

func (t *taker) open(p string) (fs.File, bool) {
	f, err := t.fsys.Open(p)
	if err != nil {
		t.report(codeReadFailed, p, err.Error())
		return nil, false
	}
	// Check the opened file too, because the entry's type comes from the directory
	// listing and the file may have been replaced since.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		t.report(codeReadFailed, p, err.Error())
		return nil, false
	}
	if !info.Mode().IsRegular() {
		f.Close()
		t.report(codePathForbidden, p, fmt.Sprintf("%s is not a regular file", p))
		return nil, false
	}
	return f, true
}

func (t *taker) hashAsset(p string) {
	f, ok := t.open(p)
	if !ok {
		return
	}
	defer f.Close()
	h := digest.New()
	n, err := io.Copy(h, ctxReader{t.ctx, f})
	if err != nil {
		t.report(codeReadFailed, p, err.Error())
		return
	}
	t.snap.assets[p] = AssetInfo{Size: n, Digest: digest.Sum(h)}
}

func (t *taker) copyFile(p string) error {
	f, ok := t.open(p)
	if !ok {
		return nil
	}
	defer f.Close()

	// Read at most one byte more than allowed, so that a file that grows while it
	// is read is still caught.
	limit := int64(-1)
	if t.limits.MaxFileBytes > 0 {
		limit = t.limits.MaxFileBytes
	}
	if t.limits.MaxTotalBytes > 0 {
		if remaining := t.limits.MaxTotalBytes - t.total; limit < 0 || remaining < limit {
			limit = remaining
		}
	}
	var r io.Reader = ctxReader{t.ctx, f}
	if limit >= 0 {
		r = io.LimitReader(r, limit+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.report(codeReadFailed, p, err.Error())
		return nil
	}
	if limit >= 0 && int64(len(data)) > limit {
		if t.limits.MaxFileBytes > 0 && int64(len(data)) > t.limits.MaxFileBytes {
			t.report(codeLimitExceeded, p, fmt.Sprintf("%s is larger than %d bytes", p, t.limits.MaxFileBytes))
			return nil
		}
		t.report(codeLimitExceeded, p, fmt.Sprintf("the bundle's files outside %s/ total more than %d bytes", AssetsDir, t.limits.MaxTotalBytes))
		return errStop
	}
	t.total += int64(len(data))
	t.snap.files[p] = data
	return nil
}

// ctxReader stops reading once ctx is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// FS returns a read-only view of the snapshot, used as bundle.Contribution.Files. It
// doesn't contain assets. Reads return copies, so nothing read through it can change
// the snapshot.
func (s *Snapshot) FS() fs.FS {
	children := map[string]map[string]bool{".": {}}
	for p := range s.files {
		for child := p; child != "."; child = path.Dir(child) {
			parent := path.Dir(child)
			if children[parent] == nil {
				children[parent] = map[string]bool{}
			}
			children[parent][path.Base(child)] = true
		}
	}
	dirs := make(map[string][]string, len(children))
	for dir, names := range children {
		sorted := make([]string, 0, len(names))
		for name := range names {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		dirs[dir] = sorted
	}
	return &snapshotFS{files: s.files, dirs: dirs}
}
