package bundle

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"slices"

	"github.com/craggy-io/foundation-kit/bundle/internal/digest"
)

// ErrAssetChanged is returned when an asset's bytes no longer match what Load
// accepted. A file that has been removed reports fs.ErrNotExist instead.
var ErrAssetChanged = errors.New("asset no longer matches the digest accepted by Load")

// Asset is one file under a bundle's assets directory, listed by a contribution in
// bundle.yaml. Load hashes it but doesn't keep its bytes, so a large asset costs
// memory only while something reads it.
//
// An Asset is an immutable value and is safe for concurrent use. Each read opens the
// Source again and checks the bytes against the accepted digest, so a read can fail
// after Load has succeeded.
type Asset struct {
	source Source
	path   string
	size   int64
	digest string
}

// Path returns the asset's slash-separated path relative to the bundle root, as
// written in bundle.yaml. It is not a path on the operating system.
func (a Asset) Path() string { return a.path }

// Size returns the asset's size in bytes, as accepted by Load.
func (a Asset) Size() int64 { return a.size }

// Digest returns the asset's content digest, as accepted by Load.
func (a Asset) Digest() string { return a.digest }

// Read returns the asset's whole contents. It returns no bytes unless they all match
// the accepted digest.
func (a Asset) Read(ctx context.Context) ([]byte, error) {
	r, err := a.Open(ctx)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(r)
	if err := errors.Join(err, r.Close()); err != nil {
		return nil, err
	}
	return data, nil
}

// Open returns a stream of the asset's contents, which the caller must close. Use it
// for assets too large to hold in memory.
//
// Open reports a changed size immediately, but a change of content only when the
// stream reaches EOF. Bytes already read are unverified until then, so a caller that
// stops early, or that acts on bytes before reaching EOF, hasn't verified them. The
// stream also stops with ctx's error if ctx is cancelled.
func (a Asset) Open(ctx context.Context) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, a.wrap(err)
	}
	fsys, closeSource, err := a.source.Open(ctx)
	if err != nil {
		return nil, a.wrap(err)
	}
	file, err := fsys.Open(a.path)
	if err != nil {
		return nil, errors.Join(a.wrap(err), closeSource())
	}
	closeAll := func() error { return errors.Join(file.Close(), closeSource()) }

	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(a.wrap(err), closeAll())
	}
	if !info.Mode().IsRegular() || info.Size() != a.size {
		return nil, errors.Join(a.wrap(ErrAssetChanged), closeAll())
	}
	return &assetReader{ctx: ctx, asset: a, file: file, closeAll: closeAll, hash: digest.New()}, nil
}

func (a Asset) wrap(err error) error {
	return fmt.Errorf("bundle: asset %s: %w", a.path, err)
}

// assetReader streams an asset and checks its bytes as they pass through.
type assetReader struct {
	ctx      context.Context
	asset    Asset
	file     fs.File
	closeAll func() error
	hash     hash.Hash
	total    int64
}

func (r *assetReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, r.asset.wrap(err)
	}
	n, err := r.file.Read(p)
	r.hash.Write(p[:n])
	r.total += int64(n)

	// The file grew after Open, so stop before streaming more than was accepted.
	if r.total > r.asset.size {
		return 0, r.asset.wrap(ErrAssetChanged)
	}
	if errors.Is(err, io.EOF) {
		if r.total != r.asset.size || digest.Sum(r.hash) != r.asset.digest {
			return n, r.asset.wrap(ErrAssetChanged)
		}
		return n, io.EOF
	}
	if err != nil {
		return n, r.asset.wrap(err)
	}
	return n, nil
}

func (r *assetReader) Close() error {
	if err := r.closeAll(); err != nil {
		return r.asset.wrap(err)
	}
	return nil
}

// Asset returns the contribution's asset at path, as written in bundle.yaml.
func (c Contribution) Asset(path string) (Asset, bool) { return findAsset(c.Assets, path) }

// Asset returns the definition's asset at path, as written in bundle.yaml.
func (d Definition[T]) Asset(path string) (Asset, bool) { return findAsset(d.Assets, path) }

func findAsset(assets []Asset, path string) (Asset, bool) {
	i := slices.IndexFunc(assets, func(a Asset) bool { return a.path == path })
	if i < 0 {
		return Asset{}, false
	}
	return assets[i], true
}
