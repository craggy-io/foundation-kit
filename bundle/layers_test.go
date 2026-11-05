package bundle_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/bundletest"
	"github.com/craggy-io/foundation-kit/bundle/compile"
	"github.com/craggy-io/foundation-kit/kind"
)

// agent is a global kind: consumers look agents up by name alone.
var agent = kind.New[string]("example.agent", 1, kind.Global())

var agentText = bundle.Compile(agent, compile.Text())

func layers(overrides ...bundle.BundleOverride) bundle.Options {
	return bundle.Options{
		Sources: []bundle.Source{
			bundle.Dir("testdata/layers/shipped"),
			bundle.Dir("testdata/layers/user"),
			bundle.Dir("testdata/layers/project"),
		},
		Compilers:       []bundle.AnyCompiler{agentText},
		BundleOverrides: overrides,
	}
}

var chain = []bundle.BundleOverride{
	{Target: "layers.shipped", Replacement: "layers.user"},
	{Target: "layers.user", Replacement: "layers.project"},
}

func TestBundleOverrideChain(t *testing.T) {
	c, err := load(t, layers(chain...))
	if err != nil {
		t.Fatal(err)
	}

	// Each contribution resolves to the highest layer that has its ID.
	for id, want := range map[string]string{
		"reviewer": "project reviewer",
		"planner":  "user planner",
		"tester":   "shipped tester",
		"auditor":  "project auditor",
	} {
		def, ok := bundle.Lookup(c, agent, id)
		if !ok || def.Value != want {
			t.Errorf("Lookup(%s) = %+v, %v; want %q", id, def, ok, want)
		}
	}

	// Get on a target reads through to the replacement.
	shipped := bundle.Ref{Owner: "layers.shipped", ID: "reviewer"}
	if def, _ := bundle.Get(c, agent, shipped); def.Value != "project reviewer" || def.Ref != shipped || def.Owner.ID != "layers.project" {
		t.Errorf("Get(%v) = %+v", shipped, def)
	}

	// All lists each effective definition once, under its own Ref.
	var refs []string
	for _, def := range bundle.All(c, agent) {
		refs = append(refs, def.Ref.String())
	}
	wantRefs := []string{"layers.project#auditor", "layers.project#reviewer", "layers.shipped#tester", "layers.user#planner"}
	if !reflect.DeepEqual(refs, wantRefs) {
		t.Errorf("All = %v, want %v", refs, wantRefs)
	}

	ref := func(owner, id string) bundle.Ref { return bundle.Ref{Owner: owner, ID: id} }
	wantOverrides := []bundle.Override{
		{Target: ref("layers.shipped", "planner"), Replacement: ref("layers.user", "planner")},
		{Target: ref("layers.shipped", "reviewer"), Replacement: ref("layers.project", "reviewer")},
		{Target: ref("layers.user", "reviewer"), Replacement: ref("layers.project", "reviewer")},
	}
	if got := c.Overrides(); !reflect.DeepEqual(got, wantOverrides) {
		t.Errorf("Overrides = %+v, want %+v", got, wantOverrides)
	}
	if got := c.Explain(shipped); !strings.Contains(got, "replaced by layers.project#reviewer (bundle override of layers.shipped by layers.user)") {
		t.Errorf("Explain(%v) =\n%s", shipped, got)
	}
	if got := c.Explain(ref("layers.project", "reviewer")); !strings.Contains(got, "replaces layers.user#reviewer (bundle override of layers.user by layers.project)") {
		t.Errorf("Explain(project reviewer) =\n%s", got)
	}

	// Without the chain, the shared IDs of a global kind conflict.
	_, err = load(t, layers())
	bundletest.RequireCode(t, err, bundle.CodeIDConflict)
}

func TestBundleOverrideChangesDigest(t *testing.T) {
	one, err := load(t, layers(bundle.BundleOverride{Target: "layers.shipped", Replacement: "layers.user"},
		bundle.BundleOverride{Target: "layers.user", Replacement: "layers.project"}))
	if err != nil {
		t.Fatal(err)
	}
	two, err := load(t, layers(bundle.BundleOverride{Target: "layers.shipped", Replacement: "layers.project"},
		bundle.BundleOverride{Target: "layers.project", Replacement: "layers.user"}))
	if err != nil {
		t.Fatal(err)
	}
	if one.Digest() == two.Digest() {
		t.Error("different bundle overrides give the same catalog digest")
	}
}

func TestBundleOverrideRejects(t *testing.T) {
	tests := []struct {
		name      string
		overrides []bundle.BundleOverride
	}{
		{"unknown target", []bundle.BundleOverride{{Target: "layers.gone", Replacement: "layers.user"}}},
		{"unknown replacement", []bundle.BundleOverride{{Target: "layers.user", Replacement: "layers.gone"}}},
		{"self", []bundle.BundleOverride{{Target: "layers.user", Replacement: "layers.user"}}},
		{"target twice", []bundle.BundleOverride{
			{Target: "layers.shipped", Replacement: "layers.user"},
			{Target: "layers.shipped", Replacement: "layers.project"},
		}},
		{"cycle", []bundle.BundleOverride{
			{Target: "layers.shipped", Replacement: "layers.user"},
			{Target: "layers.user", Replacement: "layers.project"},
			{Target: "layers.project", Replacement: "layers.shipped"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(t, layers(tt.overrides...))
			bundletest.RequireCode(t, err, bundle.CodeOverrideInvalid)
		})
	}
}

func TestBundleOverrideConflictsWithExplicitOverride(t *testing.T) {
	o := layers(chain...)
	o.Overrides = []bundle.Override{{
		Target:      bundle.Ref{Owner: "layers.shipped", ID: "reviewer"},
		Replacement: bundle.Ref{Owner: "layers.user", ID: "planner"},
	}}
	_, err := load(t, o)
	bundletest.RequireCode(t, err, bundle.CodeOverrideInvalid)
}

func TestBundleOverrideOfABuiltin(t *testing.T) {
	c, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Dir("testdata/conflict/a")},
		Compilers: []bundle.AnyCompiler{agentText},
		Providers: []kind.Provider{{ID: "example.defaults", Version: "1", Builtins: []kind.AnyBuiltin{
			kind.Builtin(agent, "reviewer", "built-in reviewer"),
			kind.Builtin(agent, "helper", "built-in helper"),
		}}},
		BundleOverrides: []bundle.BundleOverride{{Target: "example.defaults", Replacement: "conflict.a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if def, _ := bundle.Lookup(c, agent, "reviewer"); def.Value != "a reviewer" {
		t.Errorf("Lookup(reviewer) = %+v", def)
	}
	if def, _ := bundle.Lookup(c, agent, "helper"); def.Value != "built-in helper" {
		t.Errorf("Lookup(helper) = %+v", def)
	}
}

func TestGlobalIDConflict(t *testing.T) {
	o := bundle.Options{
		Sources:   []bundle.Source{bundle.Dir("testdata/conflict/a"), bundle.Dir("testdata/conflict/b")},
		Compilers: []bundle.AnyCompiler{agentText},
	}
	_, err := load(t, o)
	bundletest.RequireCode(t, err, bundle.CodeIDConflict)
	le := err.(*bundle.LoadError)
	if len(le.Diagnostics) != 2 || le.Diagnostics[0].Bundle != "conflict.a" || le.Diagnostics[1].Bundle != "conflict.b" {
		t.Errorf("diagnostics = %+v, want one for each definition", le.Diagnostics)
	}

	// An explicit override settles it.
	o.Overrides = []bundle.Override{{
		Target:      bundle.Ref{Owner: "conflict.a", ID: "reviewer"},
		Replacement: bundle.Ref{Owner: "conflict.b", ID: "reviewer"},
	}}
	c, err := load(t, o)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := bundle.Lookup(c, agent, "reviewer")
	if !ok || def.Ref.Owner != "conflict.b" || def.Value != "b reviewer" {
		t.Errorf("Lookup = %+v, %v", def, ok)
	}

	// A kind that isn't global allows the same ID in two bundles, and has no Lookup.
	local := kind.New[string]("example.agent", 1)
	c, err = load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Dir("testdata/conflict/a"), bundle.Dir("testdata/conflict/b")},
		Compilers: []bundle.AnyCompiler{bundle.Compile(local, compile.Text())},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bundle.Lookup(c, local, "reviewer"); ok {
		t.Error("Lookup succeeded for a kind that isn't global")
	}
	if _, ok := bundle.Lookup(c, agent, "nobody"); ok {
		t.Error("Lookup succeeded for a missing ID")
	}
}

// loader reads "dependency:path" from its contribution, then that file from the
// declared dependency.
var loaderKind = kind.New[string]("example.loader", 1)

func readDependency(ctx context.Context, c bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
	data, err := fs.ReadFile(c.Files, c.Path)
	if err != nil {
		return bundle.Compiled[string]{}, []bundle.Diagnostic{{Message: err.Error()}}
	}
	dep, path, _ := strings.Cut(strings.TrimSpace(string(data)), ":")
	files, id, ok := c.Dependency(dep)
	if !ok {
		return bundle.Compiled[string]{}, []bundle.Diagnostic{{Message: "no dependency " + dep}}
	}
	lib, err := fs.ReadFile(files, path)
	if err != nil {
		return bundle.Compiled[string]{}, []bundle.Diagnostic{{Message: err.Error()}}
	}
	return bundle.Compiled[string]{Value: id.ID + " " + id.Digest[:13] + " " + string(lib)}, nil
}

func TestContributionDependency(t *testing.T) {
	var seen bundle.Contribution
	capture := bundle.Compile(loaderKind, func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
		seen = c
		return readDependency(ctx, c)
	})
	c, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Dir("testdata/deps/app"), bundle.Dir("testdata/deps/lib")},
		Compilers: []bundle.AnyCompiler{capture, bundle.Compile(script, compile.Text())},
		Providers: []kind.Provider{{ID: "example.defaults", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := bundle.Get(c, loaderKind, bundle.Ref{Owner: "deps.app", ID: "main"})
	if !strings.HasPrefix(def.Value, "deps.lib sha256:") || !strings.HasSuffix(def.Value, "return s.upper()\n") {
		t.Errorf("value = %q", def.Value)
	}

	// Only declared dependencies are visible: not undeclared bundles or providers.
	if _, _, ok := seen.Dependency("example.defaults"); ok {
		t.Error("Dependency returned an undeclared provider")
	}
	if _, _, ok := seen.Dependency("deps.app"); ok {
		t.Error("Dependency returned the contribution's own bundle")
	}
}

func TestContributionDependencyMissing(t *testing.T) {
	_, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Dir("testdata/deps/app")},
		Compilers: []bundle.AnyCompiler{bundle.Compile(loaderKind, readDependency)},
	})
	// The compiler can't see a dependency that isn't loaded, so it fails.
	bundletest.RequireCode(t, err, bundle.CodeCompileFailed)
}

func TestChildren(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "b/bundle.yaml", "x")
	write(t, dir, "a/bundle.yaml", "x")
	write(t, dir, "no-manifest/readme.txt", "x")
	write(t, dir, ".hidden/bundle.yaml", "x")
	write(t, dir, "file.txt", "x")

	sources, err := bundle.Children(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var origins []string
	for _, s := range sources {
		origins = append(origins, s.Origin())
	}
	want := []string{filepath.Join(dir, "a"), filepath.Join(dir, "b")}
	if !reflect.DeepEqual(origins, want) {
		t.Errorf("Children = %v, want %v", origins, want)
	}

	sources, err = bundle.Children(ctx, filepath.Join(dir, "missing"))
	if err != nil || len(sources) != 0 {
		t.Errorf("Children(missing) = %v, %v; want nothing", sources, err)
	}
}
