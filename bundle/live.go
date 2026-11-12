package bundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing/fstest"
	"time"

	"github.com/craggy-io/foundation-kit/bundle/internal/manifest"
	"github.com/craggy-io/foundation-kit/bundle/internal/snapshot"
	"github.com/craggy-io/foundation-kit/kind"
)

// Live holds a host's current Catalog and replaces it, whole, when content changes.
// Reloads, installs and uninstalls all run the same pipeline as Load, so there is one
// way to read bundles however they arrive.
//
// Every change is a complete Load. If it fails, the previous Catalog stays current and
// the *LoadError is returned, so a running host never sees a partial catalog. On
// success the new Catalog is swapped in atomically, and subscribers are told.
//
// A reload reuses the compiled value of a contribution whose bundle digest, origin,
// declared dependencies' digests and kind are unchanged, so it only calls compilers
// for what changed. Every value is still checked against its kind's schema.
//
// Live is safe for concurrent use. See the package documentation for its rules.
type Live struct {
	options func(context.Context) (Options, error)
	current atomic.Pointer[Catalog]

	// mu serializes Reload, Install, Uninstall and Edit, including their calls to the
	// options function and to subscribers.
	mu sync.Mutex

	subMu   sync.Mutex
	subs    map[int]func(old, new *Catalog)
	nextSub int
}

// NewLive loads the first Catalog and returns a Live holding it. It returns an error,
// and no Live, unless that first load succeeds.
//
// options is called on every load, so it can list install folders again with
// Children, and pick up bundles added or removed since the last load. It must not
// call Reload, Install, Uninstall or Edit.
func NewLive(ctx context.Context, options func(context.Context) (Options, error)) (*Live, error) {
	if options == nil {
		return nil, errors.New("bundle: NewLive needs an options function")
	}
	l := &Live{options: options, subs: map[int]func(old, new *Catalog){}}
	o, err := l.loadOptions(ctx)
	if err != nil {
		return nil, err
	}
	c, err := load(ctx, o, nil)
	if err != nil {
		return nil, err
	}
	l.current.Store(c)
	return l, nil
}

// Catalog returns the current Catalog. It never blocks, and the result never
// changes, so read everything a task needs from one Catalog rather than calling
// Catalog again part-way through.
func (l *Live) Catalog() *Catalog { return l.current.Load() }

// Reload calls the options function and Load again. If the result differs from the
// current Catalog, by Catalog.Digest or by any owner's Identity (an origin can move
// without changing the digest), it becomes current, subscribers are told, and Reload
// reports true.
//
// If the options function or Load fails, the current Catalog is kept and the error
// is returned: a *LoadError, the options function's error, or ctx's error.
func (l *Live) Reload(ctx context.Context) (changed bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	o, err := l.loadOptions(ctx)
	if err != nil {
		return false, err
	}
	next, err := load(ctx, o, l.current.Load())
	if err != nil {
		return false, err
	}
	return l.swap(next), nil
}

// Run calls Reload every interval until ctx is cancelled, then returns nil. A failed
// reload doesn't stop it: the error goes to onError, if it isn't nil, and the
// previous Catalog stays current. It returns an error straight away if interval
// isn't positive.
func (l *Live) Run(ctx context.Context, interval time.Duration, onError func(error)) error {
	if interval <= 0 {
		return fmt.Errorf("bundle: Run needs a positive interval, not %v", interval)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := l.Reload(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
	}
}

// Subscribe calls fn after every change of the current Catalog, with the catalog it
// replaced and the new one, and returns a function that stops the calls.
//
// Calls happen synchronously, in subscription order, on the goroutine that made the
// change and before Reload, Install or Uninstall returns. fn may call Catalog,
// Subscribe and the unsubscribe function, but must not call Reload, Install or
// Uninstall, which would deadlock. Start long work on another goroutine.
func (l *Live) Subscribe(fn func(old, new *Catalog)) (unsubscribe func()) {
	l.subMu.Lock()
	defer l.subMu.Unlock()
	id := l.nextSub
	l.nextSub++
	l.subs[id] = fn
	var once sync.Once
	return func() {
		once.Do(func() {
			l.subMu.Lock()
			defer l.subMu.Unlock()
			delete(l.subs, id)
		})
	}
}

// Install adds the bundle in files to dir, as dir/<metadata.id>, replacing a bundle
// already installed there, and makes it current. It is a validated reload:
//
//  1. files must have a bundle.yaml at its root. Install never generates one.
//  2. The files are copied to a staging folder under dir, whose name starts with "."
//     so that Children skips it, and metadata.id is read to choose the destination.
//  3. Any bundle already at dir/<metadata.id> is moved into the staging folder, and
//     the new one is moved into its place.
//  4. The options function and Load run, so the host sees the folder exactly as it
//     will be, and can apply its own rules, such as BundleOverrides, to the new
//     bundle. Load must succeed, and its catalog must list the bundle at
//     dir/<metadata.id>, which means the options function must list dir, for example
//     with Children.
//
// If any step fails, the previous folder is put back, the staged files are removed,
// the current Catalog is kept, and the error is returned, usually a *LoadError.
// Install returns the installed bundle's Identity.
//
// Live serializes its own changes, but doesn't lock dir against other processes,
// which can see the new folder while it is being validated. A process that stops
// part-way through can leave the new folder in place and the previous one in a
// hidden ".install-*" folder.
func (l *Live) Install(ctx context.Context, dir string, files fs.FS) (Identity, error) {
	if files == nil {
		return Identity{}, errors.New("bundle: install: no files given")
	}
	if _, err := fs.Stat(files, manifest.FileName); err != nil {
		return Identity{}, &LoadError{Diagnostics: []Diagnostic{{Code: CodeManifestMissing, Severity: SeverityError,
			Path:    manifest.FileName,
			Message: fmt.Sprintf("the files to install have no %s at their root; write one, because Install never generates it", manifest.FileName)}}}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.install(ctx, dir, files)
}

// install is Install with l.mu held.
func (l *Live) install(ctx context.Context, dir string, files fs.FS) (Identity, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Identity{}, fmt.Errorf("bundle: install: %w", err)
	}
	stage, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return Identity{}, fmt.Errorf("bundle: install: %w", err)
	}
	defer os.RemoveAll(stage)
	candidate := filepath.Join(stage, "bundle")
	if err := os.CopyFS(candidate, snapshot.Visible(files)); err != nil {
		return Identity{}, fmt.Errorf("bundle: install: copying files: %w", err)
	}

	id, err := stagedID(ctx, candidate)
	if err != nil {
		return Identity{}, err
	}
	final := filepath.Join(dir, id)
	m := move{stage: stage, final: final}
	if err := m.in(candidate); err != nil {
		return Identity{}, err
	}
	o, err := l.loadOptions(ctx)
	if err != nil {
		return Identity{}, m.rollback(err)
	}
	next, err := load(ctx, o, l.current.Load())
	if err != nil {
		return Identity{}, m.rollback(err)
	}
	installed, ok := ownerAt(next, id, final)
	if !ok {
		return Identity{}, m.rollback(fmt.Errorf("bundle: install: the options function doesn't list %s; "+
			"list %s, for example with Children", final, dir))
	}
	l.swap(next)
	return installed, nil
}

// Uninstall removes the bundle installed as dir/<id>, and makes the catalog without
// it current. Like Install, it is a validated reload: the folder is moved aside, the
// options function and Load run, and if they fail it is put back and nothing
// changes. For example, removing a bundle that another bundle declares as a
// dependency fails with CodeDependencyMissing.
//
// The bundle must be in the current Catalog, with ID id and a Dir source at
// dir/<id>. Otherwise Uninstall returns an error matching fs.ErrNotExist.
func (l *Live) Uninstall(ctx context.Context, dir, id string) error {
	if !manifest.ValidID(id) {
		return fmt.Errorf("bundle: uninstall: %q is not a valid bundle ID", id)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.uninstall(ctx, dir, id)
}

// uninstall is Uninstall with l.mu held.
func (l *Live) uninstall(ctx context.Context, dir, id string) error {
	final := filepath.Join(dir, id)
	if _, ok := ownerAt(l.current.Load(), id, final); !ok {
		return fmt.Errorf("bundle: uninstall: %s is not installed at %s: %w", id, final, fs.ErrNotExist)
	}
	stage, err := os.MkdirTemp(dir, ".uninstall-")
	if err != nil {
		return fmt.Errorf("bundle: uninstall: %w", err)
	}
	defer os.RemoveAll(stage)
	m := move{stage: stage, final: final}
	if err := m.in(""); err != nil {
		return err
	}
	o, err := l.loadOptions(ctx)
	if err != nil {
		return m.rollback(err)
	}
	next, err := load(ctx, o, l.current.Load())
	if err != nil {
		return m.rollback(err)
	}
	if _, still := ownerAt(next, id, final); still {
		return m.rollback(fmt.Errorf("bundle: uninstall: %s is still listed after removing it", final))
	}
	l.swap(next)
	return nil
}

// Put is one contribution for Live.Edit to add or replace: ID, Kind and Path go in
// bundle.yaml, and Data is written to Path.
type Put struct {
	ID   string
	Kind kind.ID
	Path string
	Data []byte
}

// Edit adds, replaces and removes contributions of the bundle installed as dir/<id>,
// each named by the caller, and installs the result. It is explicit editing, for a
// host that installs what a user asked for: it never derives a manifest from files.
//
// Edit reads the bundle from dir/<id>, or starts one whose metadata.id is id and
// metadata.version is 1.0.0. Each Put sets that contribution's kind and path and
// writes Data there; its path must not be another contribution's. Each ID in remove
// must be declared, and is dropped. A file no contribution declares any more is
// deleted. Metadata, dependencies, assets and other files are kept, but comments
// in bundle.yaml are not.
//
// The result goes through Install, or Uninstall if no contribution is left, in the
// same turn, so it is validated and rolled back the same way. Edit returns the
// installed bundle's Identity, or none after Uninstall.
func (l *Live) Edit(ctx context.Context, dir, id string, put []Put, remove []string) (Identity, error) {
	if !manifest.ValidID(id) {
		return Identity{}, fmt.Errorf("bundle: edit: %q is not a valid bundle ID", id)
	}
	for _, p := range put {
		if !manifest.ValidID(p.ID) || !fs.ValidPath(p.Path) || p.Path == "." {
			return Identity{}, fmt.Errorf("bundle: edit: contribution %q at %q: the ID or path is not valid", p.ID, p.Path)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	path := filepath.Join(dir, id)
	files, m, err := readInstalled(ctx, path, id)
	if err != nil {
		return Identity{}, err
	}
	entries := m.Entries()
	var dropped []string
	for _, r := range remove {
		i := slices.IndexFunc(entries, func(c manifest.Contribution) bool { return c.ID == r })
		if i < 0 {
			return Identity{}, fmt.Errorf("bundle: edit: %s declares no contribution %q: %w", id, r, fs.ErrNotExist)
		}
		dropped = append(dropped, entries[i].Path)
		entries = slices.Delete(entries, i, i+1)
	}
	for _, p := range put {
		i := slices.IndexFunc(entries, func(c manifest.Contribution) bool { return c.ID == p.ID })
		if j := slices.IndexFunc(entries, func(c manifest.Contribution) bool { return c.Path == p.Path }); j >= 0 && j != i {
			return Identity{}, fmt.Errorf("bundle: edit: %s is contribution %q's path, not %q's", p.Path, entries[j].ID, p.ID)
		}
		if i < 0 {
			entries = append(entries, manifest.Contribution{ID: p.ID})
			i = len(entries) - 1
		}
		dropped = append(dropped, entries[i].Path)
		entries[i].Kind, entries[i].Path = p.Kind.String(), p.Path
		files[p.Path] = &fstest.MapFile{Data: p.Data, Mode: 0o644}
	}
	for _, p := range dropped {
		if !slices.ContainsFunc(entries, func(c manifest.Contribution) bool { return c.Path == p }) {
			delete(files, p)
		}
	}
	if len(entries) == 0 {
		return Identity{}, l.uninstall(ctx, dir, id)
	}
	m.SetEntries(entries)
	data, err := manifest.Encode(m)
	if err != nil {
		return Identity{}, fmt.Errorf("bundle: edit: %w", err)
	}
	files[manifest.FileName] = &fstest.MapFile{Data: data, Mode: 0o644}
	return l.install(ctx, dir, files)
}

// readInstalled returns the files and parsed manifest of the bundle at path, or a
// new manifest for id if there is no folder there.
func readInstalled(ctx context.Context, path, id string) (fstest.MapFS, *manifest.Manifest, error) {
	files := fstest.MapFS{}
	root, err := os.OpenRoot(path)
	if errors.Is(err, fs.ErrNotExist) {
		return files, &manifest.Manifest{APIVersion: manifest.APIVersion, Metadata: manifest.Metadata{ID: id, Version: "1.0.0"}}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("bundle: edit: %w", err)
	}
	defer root.Close()
	installed := snapshot.Visible(root.FS())
	err = fs.WalkDir(installed, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil || d.IsDir():
			return err
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", p)
		}
		data, err := fs.ReadFile(installed, p)
		files[p] = &fstest.MapFile{Data: data, Mode: 0o644}
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("bundle: edit: reading %s: %w", path, err)
	}
	var data []byte
	if f, ok := files[manifest.FileName]; ok {
		data = f.Data
	}
	m, diags := manifest.Parse(data)
	if len(diags) > 0 || m.Metadata.ID != id {
		if _, err := Load(ctx, Options{Sources: []Source{Dir(path)}}); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("bundle: edit: %s/%s doesn't declare metadata.id %s", path, manifest.FileName, id)
	}
	return files, m, nil
}

// move replaces the folder at final, keeping the previous one in stage so that it
// can be put back.
type move struct {
	stage, final string
	previous     string // where the previous folder was kept, if there was one
	placed       bool   // whether a new folder was moved to final
}

// in moves any folder at final aside, then moves candidate to final, unless
// candidate is "".
func (m *move) in(candidate string) error {
	if _, err := os.Lstat(m.final); err == nil {
		m.previous = filepath.Join(m.stage, "previous")
		if err := os.Rename(m.final, m.previous); err != nil {
			m.previous = ""
			return fmt.Errorf("bundle: moving %s aside: %w", m.final, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("bundle: %w", err)
	}
	if candidate == "" {
		return nil
	}
	if err := os.Rename(candidate, m.final); err != nil {
		return m.rollback(fmt.Errorf("bundle: moving %s into place: %w", m.final, err))
	}
	m.placed = true
	return nil
}

// rollback undoes in, and returns cause joined with any error doing so.
func (m *move) rollback(cause error) error {
	errs := []error{cause}
	if m.placed {
		if err := os.Rename(m.final, filepath.Join(m.stage, "rejected")); err != nil {
			errs = append(errs, fmt.Errorf("bundle: rolling back %s: %w", m.final, err))
		}
	}
	if m.previous != "" {
		if err := os.Rename(m.previous, m.final); err != nil {
			errs = append(errs, fmt.Errorf("bundle: restoring the previous %s: %w", m.final, err))
		}
	}
	return errors.Join(errs...)
}

func (l *Live) loadOptions(ctx context.Context) (Options, error) {
	if err := ctx.Err(); err != nil {
		return Options{}, err
	}
	o, err := l.options(ctx)
	if err != nil {
		return Options{}, fmt.Errorf("bundle: options: %w", err)
	}
	return o, nil
}

// swap makes next current if it differs, and tells subscribers. l.mu must be held.
func (l *Live) swap(next *Catalog) bool {
	old := l.current.Load()
	if old.digest == next.digest && slices.Equal(old.owners, next.owners) {
		return false
	}
	l.current.Store(next)

	l.subMu.Lock()
	ids := make([]int, 0, len(l.subs))
	for id := range l.subs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	fns := make([]func(old, new *Catalog), 0, len(ids))
	for _, id := range ids {
		fns = append(fns, l.subs[id])
	}
	l.subMu.Unlock()

	for _, fn := range fns {
		fn(old, next)
	}
	return true
}

// stagedID returns the metadata.id of the staged bundle at dir, which chooses where
// it is installed. If that can't be read, it runs Load on the bundle alone, so that
// the error reports every problem the same way a reload would.
func stagedID(ctx context.Context, dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifest.FileName))
	if err == nil {
		if m, _ := manifest.Parse(data); manifest.ValidID(m.Metadata.ID) {
			return m.Metadata.ID, nil
		}
	}
	if _, err := Load(ctx, Options{Sources: []Source{Dir(dir)}}); err != nil {
		return "", err
	}
	return "", fmt.Errorf("bundle: install: can't read metadata.id from %s", manifest.FileName)
}

// ownerAt returns c's owner with ID id, if its origin is the directory path.
func ownerAt(c *Catalog, id, path string) (Identity, bool) {
	for _, o := range c.owners {
		if o.ID == id && o.Origin != builtinOrigin && samePath(o.Origin, path) {
			return o, true
		}
	}
	return Identity{}, false
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}
