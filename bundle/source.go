package bundle

import (
	"context"
	"io/fs"
)

// Source supplies the files for exactly one bundle. The root of the file system it
// opens must contain bundle.yaml.
//
// A Source only differs in how it acquires bytes. Load applies the same manifest,
// path and snapshot rules to every Source, so a new Source (an archive or a stored
// artifact, for example) must not add its own format or discovery rules. Anything
// remote should be fetched and verified before it is presented as a Source.
type Source interface {
	// Origin describes where the bundle came from, for diagnostics and
	// Catalog.Explain. It is excluded from all digests, so moving a checkout does
	// not change a bundle's identity.
	Origin() string

	// Open returns a file system rooted at the bundle directory, and a function
	// that releases it. Load copies what it accepts into a snapshot and then calls
	// closeFn.
	//
	// Every read of an Asset calls Open again and then closeFn, so a Source must keep
	// working for as long as its Catalog is in use, and must support concurrent calls.
	Open(ctx context.Context) (fsys fs.FS, closeFn func() error, err error)
}

// Dir returns a Source for the bundle directory at path. The directory itself must
// contain bundle.yaml, because Dir does not search subdirectories for bundles.
func Dir(path string) Source { return dirSource{path: path} }

type dirSource struct{ path string }

func (s dirSource) Origin() string { return s.path }

// Open returns the directory as a file system.
//
// TODO: implement with os.OpenRoot rather than a bare os.DirFS, which can follow a
// symlink out of the directory. Return the os.Root's Close method as closeFn.
// internal/snapshot still rejects symlinks either way.
func (s dirSource) Open(ctx context.Context) (fs.FS, func() error, error) {
	return nil, nil, ErrNotImplemented
}

// Embedded returns a Source for a bundle held in fsys, such as an embed.FS or a
// fstest.MapFS. The root of fsys must contain bundle.yaml, so use fs.Sub when the
// bundle sits in a subdirectory. name appears in diagnostics as the Origin.
func Embedded(name string, fsys fs.FS) Source {
	return embeddedSource{name: name, fsys: fsys}
}

type embeddedSource struct {
	name string
	fsys fs.FS
}

func (s embeddedSource) Origin() string { return s.name }

// Open returns the file system unchanged. There is nothing to release, because the
// caller owns fsys.
func (s embeddedSource) Open(ctx context.Context) (fs.FS, func() error, error) {
	return s.fsys, func() error { return nil }, nil
}
