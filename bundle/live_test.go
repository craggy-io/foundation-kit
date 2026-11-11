package bundle_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/bundletest"
	"github.com/craggy-io/foundation-kit/bundle/compile"
)

func write(t *testing.T, dir, path, data string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
}

// host is a typical Live host: shipped bundles, plus whatever is installed in a
// folder, listed again on every load.
type host struct {
	shipped   []bundle.Source
	installed string
	compiles  atomic.Int64 // calls of the agent compiler
	fail      atomic.Bool  // makes the options function fail
}

func newHost(t *testing.T, shipped ...string) *host {
	h := &host{installed: filepath.Join(t.TempDir(), "installed")}
	for _, s := range shipped {
		h.shipped = append(h.shipped, bundle.Dir(s))
	}
	return h
}

func (h *host) options(ctx context.Context) (bundle.Options, error) {
	if h.fail.Load() {
		return bundle.Options{}, errors.New("options failed")
	}
	children, err := bundle.Children(ctx, h.installed)
	if err != nil {
		return bundle.Options{}, err
	}
	text := compile.Text()
	return bundle.Options{
		Sources: append(append([]bundle.Source(nil), h.shipped...), children...),
		Compilers: []bundle.AnyCompiler{
			bundle.Compile(agent, func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
				h.compiles.Add(1)
				return text(ctx, c)
			}),
			bundle.Compile(script, compile.Text()),
			bundle.Compile(loaderKind, readDependency),
		},
	}, nil
}

func (h *host) live(t *testing.T) *bundle.Live {
	t.Helper()
	l, err := bundle.NewLive(context.Background(), h.options)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNewLiveNeedsAGoodFirstLoad(t *testing.T) {
	ctx := context.Background()
	if _, err := bundle.NewLive(ctx, nil); err == nil {
		t.Error("NewLive(nil) succeeded")
	}
	h := newHost(t, "testdata/conflict/a", "testdata/conflict/b")
	l, err := bundle.NewLive(ctx, h.options)
	if l != nil {
		t.Error("NewLive returned a Live after a failed load")
	}
	bundletest.RequireCode(t, err, bundle.CodeIDConflict)
}

func TestLiveReload(t *testing.T) {
	ctx := context.Background()
	h := newHost(t)
	copyDir(t, "testdata/conflict/a", filepath.Join(h.installed, "a"))
	l := h.live(t)
	first := l.Catalog()
	if h.compiles.Load() != 1 {
		t.Fatalf("compiles = %d, want 1", h.compiles.Load())
	}

	var calls []string
	unsubscribe := l.Subscribe(func(old, new *bundle.Catalog) {
		if old == new || l.Catalog() != new {
			t.Error("subscriber called with the wrong catalogs")
		}
		calls = append(calls, new.Digest())
	})

	// Nothing changed: no swap, no compile.
	changed, err := l.Reload(ctx)
	if err != nil || changed || l.Catalog() != first || h.compiles.Load() != 1 {
		t.Fatalf("Reload = %v, %v; catalog swapped %v; compiles %d", changed, err, l.Catalog() != first, h.compiles.Load())
	}

	// A new bundle: only it is compiled.
	copyDir(t, "testdata/install/good", filepath.Join(h.installed, "good"))
	changed, err = l.Reload(ctx)
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if h.compiles.Load() != 2 {
		t.Errorf("compiles = %d, want 2: the unchanged bundle should be reused", h.compiles.Load())
	}
	if _, ok := bundle.Lookup(l.Catalog(), agent, "helper"); !ok || len(calls) != 1 {
		t.Errorf("new bundle missing, or subscribers called %d times", len(calls))
	}

	// A conflicting bundle: the reload fails and the last good catalog stays.
	good := l.Catalog()
	copyDir(t, "testdata/conflict/b", filepath.Join(h.installed, "b"))
	changed, err = l.Reload(ctx)
	bundletest.RequireCode(t, err, bundle.CodeIDConflict)
	if changed || l.Catalog() != good {
		t.Error("a failed reload changed the catalog")
	}

	// The options function failing also keeps it.
	h.fail.Store(true)
	if _, err := l.Reload(ctx); err == nil || l.Catalog() != good {
		t.Errorf("Reload with failing options = %v", err)
	}
	h.fail.Store(false)

	// Removing the conflict recovers, and an unsubscribed function isn't called.
	unsubscribe()
	unsubscribe() // safe to repeat
	if err := os.RemoveAll(filepath.Join(h.installed, "b")); err != nil {
		t.Fatal(err)
	}
	write(t, h.installed, "good/helper.txt", "edited helper")
	if changed, err := l.Reload(ctx); err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if def, _ := bundle.Lookup(l.Catalog(), agent, "helper"); def.Value != "edited helper" || len(calls) != 1 {
		t.Errorf("helper = %q, subscriber calls = %d", def.Value, len(calls))
	}
}

func TestLiveRecompilesDependents(t *testing.T) {
	ctx := context.Background()
	h := newHost(t)
	copyDir(t, "testdata/deps/app", filepath.Join(h.installed, "app"))
	copyDir(t, "testdata/deps/lib", filepath.Join(h.installed, "lib"))
	l := h.live(t)
	main := bundle.Ref{Owner: "deps.app", ID: "main"}
	before, _ := bundle.Get(l.Catalog(), loaderKind, main)

	// Only the dependency changes, but its dependent reads it, so it is recompiled.
	write(t, h.installed, "lib/lib/strings.star", "def shout(s):\n    return s + '!'\n")
	if changed, err := l.Reload(ctx); err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	after, _ := bundle.Get(l.Catalog(), loaderKind, main)
	if after.Value == before.Value {
		t.Errorf("dependent kept its old value %q after its dependency changed", after.Value)
	}
}

func TestLiveInstall(t *testing.T) {
	ctx := context.Background()
	h := newHost(t, "testdata/layers/shipped")
	l := h.live(t)
	var notified atomic.Int64
	l.Subscribe(func(old, new *bundle.Catalog) { notified.Add(1) })

	id, err := l.Install(ctx, h.installed, os.DirFS("testdata/install/good"))
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(h.installed, "install.good")
	if id.ID != "install.good" || id.Origin != final {
		t.Errorf("Identity = %+v", id)
	}
	def, ok := bundle.Lookup(l.Catalog(), agent, "helper")
	if !ok || def.Owner != id || notified.Load() != 1 {
		t.Fatalf("helper = %+v, %v; notified %d", def, ok, notified.Load())
	}
	// The 3 shipped agents are compiled once, and reused by Install's load.
	if got := h.compiles.Load(); got != 4 {
		t.Errorf("compiles = %d, want 4", got)
	}
	// Assets and files are read from the final folder, not the staging one.
	if _, err := os.Stat(filepath.Join(final, "helper.txt")); err != nil {
		t.Error(err)
	}
	assertOnlyInstalled(t, h.installed, "install.good")

	// A replacement that fails to load changes nothing, on disk or in memory.
	installed := l.Catalog()
	_, err = l.Install(ctx, h.installed, os.DirFS("testdata/install/broken"))
	bundletest.RequireCode(t, err, bundle.CodeCompileFailed)
	if l.Catalog() != installed || notified.Load() != 1 {
		t.Error("a failed install changed the catalog")
	}
	if data, _ := os.ReadFile(filepath.Join(final, "helper.txt")); string(data) != "installed helper" {
		t.Errorf("a failed install changed the installed files: %q", data)
	}
	assertOnlyInstalled(t, h.installed, "install.good")

	// No manifest: never generated, always an error.
	_, err = l.Install(ctx, h.installed, os.DirFS("testdata/install/no-manifest"))
	bundletest.RequireCode(t, err, bundle.CodeManifestMissing)

	// A manifest with no ID can't be placed, and is reported like any other load.
	noID := filepath.Join(t.TempDir(), "n")
	write(t, noID, "bundle.yaml", "apiVersion: craggy.io/bundle/v1\nmetadata: {version: '1'}\n")
	_, err = l.Install(ctx, h.installed, os.DirFS(noID))
	bundletest.RequireCode(t, err, bundle.CodeManifestInvalid)
	assertOnlyInstalled(t, h.installed, "install.good")

	// A bundle whose IDs conflict with a shipped one is rejected.
	conflicting := filepath.Join(t.TempDir(), "c")
	copyDir(t, "testdata/install/good", conflicting)
	write(t, conflicting, "bundle.yaml", "apiVersion: craggy.io/bundle/v1\nmetadata: {id: install.other, version: '1'}\n"+
		"contributions: [{id: reviewer, kind: example.agent@1, path: helper.txt}]\n")
	_, err = l.Install(ctx, h.installed, os.DirFS(conflicting))
	bundletest.RequireCode(t, err, bundle.CodeIDConflict)
	assertOnlyInstalled(t, h.installed, "install.good")

	// A good replacement replaces it in place.
	updated := filepath.Join(t.TempDir(), "u")
	copyDir(t, "testdata/install/good", updated)
	write(t, updated, "helper.txt", "updated helper")
	if _, err := l.Install(ctx, h.installed, os.DirFS(updated)); err != nil {
		t.Fatal(err)
	}
	if def, _ := bundle.Lookup(l.Catalog(), agent, "helper"); def.Value != "updated helper" || notified.Load() != 2 {
		t.Errorf("helper = %q, notified %d", def.Value, notified.Load())
	}
	assertOnlyInstalled(t, h.installed, "install.good")
}

func TestLiveInstallNeedsTheFolderListed(t *testing.T) {
	ctx := context.Background()
	h := newHost(t, "testdata/layers/shipped")
	l := h.live(t)
	before := l.Catalog()
	other := filepath.Join(t.TempDir(), "elsewhere")
	if _, err := l.Install(ctx, other, os.DirFS("testdata/install/good")); err == nil {
		t.Fatal("Install into a folder the options function doesn't list succeeded")
	}
	if l.Catalog() != before {
		t.Error("catalog changed")
	}
	assertOnlyInstalled(t, other)
}

func TestLiveUninstall(t *testing.T) {
	ctx := context.Background()
	h := newHost(t)
	l := h.live(t)
	if _, err := l.Install(ctx, h.installed, os.DirFS("testdata/install/good")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Install(ctx, h.installed, os.DirFS("testdata/install/dependent")); err != nil {
		t.Fatal(err)
	}

	// install.dependent depends on install.good, so it can't go first.
	before := l.Catalog()
	err := l.Uninstall(ctx, h.installed, "install.good")
	bundletest.RequireCode(t, err, bundle.CodeDependencyMissing)
	if l.Catalog() != before {
		t.Error("a failed uninstall changed the catalog")
	}
	assertOnlyInstalled(t, h.installed, "install.dependent", "install.good")

	if err := l.Uninstall(ctx, h.installed, "install.dependent"); err != nil {
		t.Fatal(err)
	}
	if err := l.Uninstall(ctx, h.installed, "install.good"); err != nil {
		t.Fatal(err)
	}
	if _, ok := bundle.Lookup(l.Catalog(), agent, "helper"); ok {
		t.Error("uninstalled bundle is still in the catalog")
	}
	assertOnlyInstalled(t, h.installed)

	if err := l.Uninstall(ctx, h.installed, "install.good"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Uninstall of a missing bundle = %v", err)
	}
	if err := l.Uninstall(ctx, h.installed, "../escape"); err == nil {
		t.Error("Uninstall accepted an invalid ID")
	}
}

// assertOnlyInstalled checks that dir holds exactly the given folders, with no
// staging folders left behind.
func TestLiveEdit(t *testing.T) {
	ctx := context.Background()
	h := newHost(t, "testdata/layers/shipped")
	l := h.live(t)
	final := filepath.Join(h.installed, "user")
	value := func(id string) string {
		def, ok := bundle.Lookup(l.Catalog(), agent, id)
		if !ok {
			return ""
		}
		return def.Value
	}

	// Create: there is no bundle yet, so Edit starts one.
	id, err := l.Edit(ctx, h.installed, "user", []bundle.Put{
		{ID: "helper", Kind: agent.ID(), Path: "agents/helper.txt", Data: []byte("v1")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "user" || id.Version != "1.0.0" || id.Origin != final || value("helper") != "v1" {
		t.Fatalf("Identity = %+v, helper = %q", id, value("helper"))
	}

	// Edits keep metadata and assets they don't name, and a moved file goes.
	write(t, final, "bundle.yaml", "# comment\napiVersion: craggy.io/bundle/v1\n"+
		"metadata: {id: user, version: '2', title: Mine}\ncontributions:\n"+
		"  - {id: helper, kind: example.agent@1, path: agents/helper.txt, assets: [assets/a.txt]}\n")
	write(t, final, "assets/a.txt", "asset")
	if _, err := l.Edit(ctx, h.installed, "user", []bundle.Put{
		{ID: "helper", Kind: agent.ID(), Path: "helper.txt", Data: []byte("v2")},
		{ID: "other", Kind: agent.ID(), Path: "other.txt", Data: []byte("other")},
	}, nil); err != nil {
		t.Fatal(err)
	}
	def, _ := bundle.Lookup(l.Catalog(), agent, "helper")
	if def.Value != "v2" || def.Owner.Version != "2" || len(def.Assets) != 1 || value("other") != "other" {
		t.Fatalf("helper = %+v, other = %q", def, value("other"))
	}
	manifest, _ := os.ReadFile(filepath.Join(final, "bundle.yaml"))
	if !strings.Contains(string(manifest), "title: Mine") {
		t.Errorf("bundle.yaml lost its title:\n%s", manifest)
	}
	if _, err := os.Stat(filepath.Join(final, "agents/helper.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old path of helper is still there: %v", err)
	}

	// Remove drops the contribution and its file.
	if _, err := l.Edit(ctx, h.installed, "user", nil, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if value("other") != "" || value("helper") != "v2" {
		t.Errorf("after remove: other = %q, helper = %q", value("other"), value("helper"))
	}
	if _, err := os.Stat(filepath.Join(final, "other.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("other.txt is still there: %v", err)
	}
	if _, err := l.Edit(ctx, h.installed, "user", nil, []string{"other"}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("removing an undeclared contribution = %v", err)
	}

	// A failing edit is rolled back: the shipped bundle already defines reviewer.
	before := l.Catalog()
	_, err = l.Edit(ctx, h.installed, "user", []bundle.Put{
		{ID: "helper", Kind: agent.ID(), Path: "helper.txt", Data: []byte("v3")},
		{ID: "reviewer", Kind: agent.ID(), Path: "reviewer.txt", Data: []byte("mine")},
	}, nil)
	bundletest.RequireCode(t, err, bundle.CodeIDConflict)
	if l.Catalog() != before {
		t.Error("a failed edit changed the catalog")
	}
	if data, _ := os.ReadFile(filepath.Join(final, "helper.txt")); string(data) != "v2" {
		t.Errorf("a failed edit changed the installed files: %q", data)
	}
	assertOnlyInstalled(t, h.installed, "user")

	// Removing the last contribution uninstalls the bundle.
	if id, err := l.Edit(ctx, h.installed, "user", nil, []string{"helper"}); err != nil || id != (bundle.Identity{}) {
		t.Fatalf("Edit = %+v, %v", id, err)
	}
	if value("helper") != "" {
		t.Error("helper is still in the catalog")
	}
	assertOnlyInstalled(t, h.installed)
}

func TestLiveEditPreservesApplicationAndTrigger(t *testing.T) {
	ctx := context.Background()
	h := newHost(t)
	l := h.live(t)
	_, err := l.Install(ctx, h.installed, fstest.MapFS{
		"bundle.yaml": {Data: []byte(`apiVersion: craggy.io/bundle/v1
metadata: {id: user.application, version: 1.0.0}
title: One application
trigger: {on: records.changed}
application: {id: helper, kind: example.agent@1, path: helper.txt}
`)},
		"helper.txt": {Data: []byte("v1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Edit(ctx, h.installed, "user.application", []bundle.Put{{ID: "helper", Kind: agent.ID(), Path: "helper.txt", Data: []byte("v2")}}, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(h.installed, "user.application", "bundle.yaml"))
	if err != nil || !strings.Contains(string(b), "application:") || !strings.Contains(string(b), "trigger:") || strings.Contains(string(b), "contributions:") {
		t.Fatalf("edited manifest = %s, %v", b, err)
	}
}

func assertOnlyInstalled(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("%s holds %v, want %v", dir, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s holds %v, want %v", dir, got, want)
		}
	}
}

func TestLiveRun(t *testing.T) {
	h := newHost(t)
	l := h.live(t)
	if err := l.Run(context.Background(), 0, nil); err == nil {
		t.Error("Run with no interval succeeded")
	}

	h.fail.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 100)
	done := make(chan error)
	go func() { done <- l.Run(ctx, time.Millisecond, func(err error) { errs <- err }) }()
	select {
	case <-errs:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't report a failing reload")
	}

	// It keeps going after an error, and picks up changes once the cause is fixed.
	h.fail.Store(false)
	copyDir(t, "testdata/install/good", filepath.Join(h.installed, "good"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := bundle.Lookup(l.Catalog(), agent, "helper"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run didn't reload")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v, want nil after cancellation", err)
	}
}

// TestLiveConcurrentUse is meant for go test -race.
func TestLiveConcurrentUse(t *testing.T) {
	ctx := context.Background()
	h := newHost(t, "testdata/layers/shipped")
	l := h.live(t)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c := l.Catalog()
				_ = bundle.All(c, agent)
				_, _ = bundle.Lookup(c, agent, "reviewer")
				unsubscribe := l.Subscribe(func(old, new *bundle.Catalog) {})
				unsubscribe()
			}
		}()
	}
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				if _, err := l.Reload(ctx); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := l.Install(ctx, h.installed, os.DirFS("testdata/install/good")); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	if _, ok := bundle.Lookup(l.Catalog(), agent, "helper"); !ok {
		t.Error("installed bundle missing")
	}
}
