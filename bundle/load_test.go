package bundle_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/bundletest"
	"github.com/craggy-io/foundation-kit/bundle/compile"
	"github.com/craggy-io/foundation-kit/kind"
)

var (
	script   = kind.New[string]("example.script", 1)
	prompt   = kind.New[string]("example.prompt", 1)
	greeting = kind.New[string]("example.greeting", 1)
)

// testCompilers handles every kind used in testdata.
var testCompilers = []bundle.AnyCompiler{
	bundle.Compile(script, compile.Text()),
	bundle.Compile(prompt, compile.Text()),
}

func load(t *testing.T, o bundle.Options) (*bundle.Catalog, error) {
	t.Helper()
	c, err := bundle.Load(context.Background(), o)
	if (c == nil) == (err == nil) {
		t.Fatalf("Load returned catalog %v and error %v; want exactly one", c, err)
	}
	return c, err
}

func TestLoadValidTestdata(t *testing.T) {
	c, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Dir("testdata/valid/example")},
		Compilers: testCompilers,
	})
	if err != nil {
		t.Fatal(err)
	}

	hello, ok := bundle.Get(c, script, bundle.Ref{Owner: "example.hello", ID: "hello"})
	if !ok || !strings.Contains(hello.Value, `return "hello"`) || len(hello.Assets) != 0 {
		t.Errorf("hello = %+v, %v", hello, ok)
	}
	review, ok := bundle.Get(c, prompt, bundle.Ref{Owner: "example.hello", ID: "review"})
	if !ok {
		t.Fatal("review not found")
	}
	if review.Owner.ID != "example.hello" || review.Owner.Version != "0.1.0" ||
		review.Owner.Origin != "testdata/valid/example" || !strings.HasPrefix(review.Owner.Digest, "sha256:") {
		t.Errorf("review.Owner = %+v", review.Owner)
	}
	tone, ok := review.Asset("assets/style/tone.md")
	if !ok {
		t.Fatal("review has no tone asset")
	}
	data, err := tone.Read(context.Background())
	if err != nil || !strings.HasPrefix(string(data), "Be direct") {
		t.Errorf("tone.Read = %q, %v", data, err)
	}
	if owners := c.Owners(); len(owners) != 1 || owners[0] != review.Owner {
		t.Errorf("Owners = %+v", owners)
	}
	if !strings.HasPrefix(c.Digest(), "sha256:") {
		t.Errorf("Digest = %q", c.Digest())
	}
}

// TestLoadInvalidTestdata loads every bundle under testdata/invalid, and checks that
// it fails with the code its bundle.yaml names on a "# Expected: <code>" line.
func TestLoadInvalidTestdata(t *testing.T) {
	dirs, err := os.ReadDir("testdata/invalid")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no invalid bundles")
	}
	for _, d := range dirs {
		t.Run(d.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata/invalid", d.Name())
			want := expectedCode(t, filepath.Join(dir, "bundle.yaml"))
			_, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Dir(dir)}, Compilers: testCompilers})
			bundletest.RequireCode(t, err, want)

			// The comments say validation fails first, so no compiler runs.
			var le *bundle.LoadError
			if errors.As(err, &le) {
				for _, d := range le.Diagnostics {
					if d.Code == bundle.CodeCompileFailed {
						t.Errorf("a compiler ran: %+v", d)
					}
				}
			}
		})
	}
}

func expectedCode(t *testing.T, manifest string) bundle.Code {
	t.Helper()
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		if rest, ok := strings.CutPrefix(s.Text(), "# Expected: "); ok {
			code, _, _ := strings.Cut(rest, ",")
			return bundle.Code(code)
		}
	}
	t.Fatalf("%s has no # Expected: line", manifest)
	return ""
}

func TestLoadReportsUnknownFieldWithItsLine(t *testing.T) {
	_, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Dir("testdata/invalid/unknown-field")}})
	var le *bundle.LoadError
	if !errors.As(err, &le) {
		t.Fatal(err)
	}
	d := le.Diagnostics[0]
	if d.Code != bundle.CodeManifestInvalid || d.Bundle != "invalid.unknown-field" || d.Path != "bundle.yaml" || d.Line != 3 {
		t.Errorf("diagnostic = %+v", d)
	}
}

func hello(extra ...string) fstest.MapFS {
	m := "apiVersion: craggy.io/bundle/v1\nmetadata: {id: example.hello, version: 0.1.0}\ncontributions:\n" +
		"  - {id: hello, kind: example.greeting@1, path: hello.txt}\n" + strings.Join(extra, "")
	return fstest.MapFS{"bundle.yaml": {Data: []byte(m)}, "hello.txt": {Data: []byte("hello")}}
}

func TestApplicationMetadataReachesCompilerAndCatalog(t *testing.T) {
	files := fstest.MapFS{
		"bundle.yaml": {Data: []byte(`apiVersion: craggy.io/bundle/v1
metadata: {id: example.application, version: 1.0.0}
title: Investigate
description: Inspect one object.
tags: [research, binary]
trigger: {on: records.changed, where: {type: elf}}
application: {id: investigate, kind: example.greeting@1, path: workflow.txt}
`)},
		"workflow.txt": {Data: []byte("body")},
	}
	compiler := bundle.Compile(greeting, func(_ context.Context, c bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
		if c.Metadata.Title != "Investigate" || string(c.Metadata.Trigger) == "" {
			t.Errorf("compiler metadata = %+v", c.Metadata)
		}
		return bundle.Compiled[string]{Value: "compiled"}, nil
	})
	cat, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Embedded("application", files)}, Compilers: []bundle.AnyCompiler{compiler}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := bundle.Get(cat, greeting, bundle.Ref{Owner: "example.application", ID: "investigate"})
	if !ok || got.Value != "compiled" || got.Metadata.Title != "Investigate" || got.Metadata.Description != "Inspect one object." || len(got.Metadata.Tags) != 2 {
		t.Fatalf("definition = %+v, found = %v", got, ok)
	}
	got.Metadata.Tags[0] = "changed"
	got.Metadata.Trigger[0] = 'x'
	again, _ := bundle.Get(cat, greeting, got.Ref)
	if again.Metadata.Tags[0] != "research" || again.Metadata.Trigger[0] != '{' {
		t.Fatalf("catalog metadata mutated: %+v", again.Metadata)
	}
}

func TestLoadBuiltinsAndOverrides(t *testing.T) {
	defaults := kind.Provider{ID: "example.defaults", Version: "1.0.0", Builtins: []kind.AnyBuiltin{
		kind.Builtin(greeting, "default", "hi"),
	}}
	target := bundle.Ref{Owner: "example.defaults", ID: "default"}
	replacement := bundle.Ref{Owner: "example.hello", ID: "hello"}
	c, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Embedded("mem", hello())},
		Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, compile.Text())},
		Providers: []kind.Provider{defaults},
		Overrides: []bundle.Override{{Target: target, Replacement: replacement}},
	})
	if err != nil {
		t.Fatal(err)
	}
	def, ok := bundle.Get(c, greeting, target)
	if !ok || def.Ref != target || def.Value != "hello" || def.Owner.ID != "example.hello" {
		t.Errorf("Get(target) = %+v, %v; want the replacement's value and owner", def, ok)
	}
	if got := c.Explain(target); !strings.Contains(got, "replaced by example.hello#hello") || !strings.Contains(got, "origin:  mem") {
		t.Errorf("Explain(target) =\n%s", got)
	}
	if got := c.Explain(replacement); !strings.Contains(got, "replaces example.defaults#default") {
		t.Errorf("Explain(replacement) =\n%s", got)
	}
	if got := c.Explain(bundle.Ref{Owner: "x", ID: "y"}); !strings.Contains(got, "not in the catalog") {
		t.Errorf("Explain(missing) = %s", got)
	}
	owners := c.Owners()
	if len(owners) != 2 || owners[0].ID != "example.defaults" || owners[0].Origin != "builtin" || owners[0].Digest != "" {
		t.Errorf("Owners = %+v", owners)
	}

	// Without the override, the same input still loads, and the digest differs.
	plain, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Embedded("mem", hello())},
		Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, compile.Text())},
		Providers: []kind.Provider{defaults},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Digest() == c.Digest() {
		t.Error("an override doesn't change the catalog digest")
	}
	if def, _ := bundle.Get(plain, greeting, target); def.Value != "hi" {
		t.Errorf("Get(target) without override = %q", def.Value)
	}
}

func TestLoadDigestIgnoresOriginAndOrder(t *testing.T) {
	other := fstest.MapFS{
		"bundle.yaml": {Data: []byte("apiVersion: craggy.io/bundle/v1\nmetadata: {id: example.other, version: '1'}\ncontributions: [{id: x, kind: example.greeting@1, path: x}]\n")},
		"x":           {Data: []byte("x")},
	}
	opts := func(sources ...bundle.Source) bundle.Options {
		return bundle.Options{Sources: sources, Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, compile.Text())}}
	}
	a, err := load(t, opts(bundle.Embedded("a", hello()), bundle.Embedded("b", other)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := load(t, opts(bundle.Embedded("elsewhere", other), bundle.Embedded("moved", hello())))
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() != b.Digest() {
		t.Error("source order or origin changed the catalog digest")
	}
	changed := hello()
	changed["hello.txt"] = &fstest.MapFile{Data: []byte("HELLO")}
	d, err := load(t, opts(bundle.Embedded("a", changed), bundle.Embedded("b", other)))
	if err != nil {
		t.Fatal(err)
	}
	if d.Digest() == a.Digest() {
		t.Error("changing a file's bytes didn't change the catalog digest")
	}
}

func TestLoadDirMatchesEmbedded(t *testing.T) {
	compilers := testCompilers
	dir, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Dir("testdata/valid/example")}, Compilers: compilers})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(os.DirFS("testdata"), "valid/example")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Embedded("e", sub)}, Compilers: compilers})
	if err != nil {
		t.Fatal(err)
	}
	if dir.Digest() != embedded.Digest() {
		t.Error("the same bundle has different digests through Dir and Embedded")
	}
}

func TestLoadRejects(t *testing.T) {
	greetingText := bundle.Compile(greeting, compile.Text())
	twin := kind.New[string]("example.greeting", 1)
	schema := kind.New[string]("example.checked", 1, kind.WithSchema(rejectAll{}))
	tests := []struct {
		name string
		opts bundle.Options
		code bundle.Code
	}{
		{"no manifest", bundle.Options{Sources: []bundle.Source{bundle.Embedded("m", fstest.MapFS{"x": {}})}}, bundle.CodeManifestMissing},
		{"missing dir", bundle.Options{Sources: []bundle.Source{bundle.Dir("testdata/does-not-exist")}}, bundle.CodeSourceUnavailable},
		{"nested manifest", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("m", merge(hello(), fstest.MapFS{"sub/bundle.yaml": {}}))},
			Compilers: []bundle.AnyCompiler{greetingText},
		}, bundle.CodeManifestNested},
		{"no compiler", bundle.Options{Sources: []bundle.Source{bundle.Embedded("m", hello())}}, bundle.CodeKindUnknown},
		{"malformed kind", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("m", hello("  - {id: x, kind: script/1, path: x}\n"))},
			Compilers: []bundle.AnyCompiler{greetingText},
		}, bundle.CodeKindInvalid},
		{"invalid compiler kind", bundle.Options{
			Compilers: []bundle.AnyCompiler{bundle.Compile(kind.New[string]("script", 1), compile.Text())},
		}, bundle.CodeKindInvalid},
		{"two compilers", bundle.Options{Compilers: []bundle.AnyCompiler{greetingText, greetingText}}, bundle.CodeDuplicateID},
		{"two kinds with one ID", bundle.Options{
			Compilers: []bundle.AnyCompiler{greetingText},
			Providers: []kind.Provider{{ID: "p", Version: "1", Builtins: []kind.AnyBuiltin{kind.Builtin(twin, "x", "x")}}},
		}, bundle.CodeKindInvalid},
		{"compile failure", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("m", hello("  - {id: x, kind: example.greeting@1, path: missing.txt}\n"))},
			Compilers: []bundle.AnyCompiler{greetingText},
		}, bundle.CodeCompileFailed},
		{"compiler panic", bundle.Options{
			Sources: []bundle.Source{bundle.Embedded("m", hello())},
			Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, func(context.Context, bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
				panic("boom")
			})},
		}, bundle.CodeCompileFailed},
		{"schema on compiled value", bundle.Options{
			Sources: []bundle.Source{bundle.Embedded("m", fstest.MapFS{
				"bundle.yaml": {Data: []byte("apiVersion: craggy.io/bundle/v1\nmetadata: {id: a, version: '1'}\ncontributions: [{id: x, kind: example.checked@1, path: x}]\n")},
				"x":           {},
			})},
			Compilers: []bundle.AnyCompiler{bundle.Compile(schema, compile.Text())},
		}, bundle.CodeSchemaInvalid},
		{"schema on built-in", bundle.Options{
			Providers: []kind.Provider{{ID: "p", Version: "1", Builtins: []kind.AnyBuiltin{kind.Builtin(schema, "x", "x")}}},
		}, bundle.CodeSchemaInvalid},
		{"provider without version", bundle.Options{Providers: []kind.Provider{{ID: "p"}}}, bundle.CodeProviderInvalid},
		{"duplicate built-in", bundle.Options{
			Providers: []kind.Provider{{ID: "p", Version: "1", Builtins: []kind.AnyBuiltin{kind.Builtin(greeting, "x", "a"), kind.Builtin(greeting, "x", "b")}}},
		}, bundle.CodeDuplicateID},
		{"duplicate bundle", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("a", hello()), bundle.Embedded("b", hello())},
			Compilers: []bundle.AnyCompiler{greetingText},
		}, bundle.CodeDuplicateID},
		{"bundle and provider share an ID", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("a", hello())},
			Compilers: []bundle.AnyCompiler{greetingText},
			Providers: []kind.Provider{{ID: "example.hello", Version: "1"}},
		}, bundle.CodeDuplicateID},
		{"missing reference", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("a", hello())},
			Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, refersTo(bundle.Ref{Owner: "example.hello", ID: "nothing"}))},
		}, bundle.CodeReferenceMissing},
		{"undeclared dependency", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("a", hello())},
			Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, refersTo(bundle.Ref{Owner: "p", ID: "x"}))},
			Providers: []kind.Provider{{ID: "p", Version: "1", Builtins: []kind.AnyBuiltin{kind.Builtin(greeting, "x", "x")}}},
		}, bundle.CodeDependencyUndeclared},
		{"missing dependency", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("a", hello("dependencies: [{id: example.gone}]\n"))},
			Compilers: []bundle.AnyCompiler{greetingText},
		}, bundle.CodeDependencyMissing},
		{"invalid override", bundle.Options{
			Sources:   []bundle.Source{bundle.Embedded("a", hello())},
			Compilers: []bundle.AnyCompiler{greetingText},
			Overrides: []bundle.Override{{Target: bundle.Ref{Owner: "x", ID: "y"}, Replacement: bundle.Ref{Owner: "example.hello", ID: "hello"}}},
		}, bundle.CodeOverrideInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(t, tt.opts)
			bundletest.RequireCode(t, err, tt.code)
		})
	}
}

func TestLoadDeclaredDependency(t *testing.T) {
	c, err := load(t, bundle.Options{
		Sources:   []bundle.Source{bundle.Embedded("a", hello("dependencies: [{id: p}]\n"))},
		Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, refersTo(bundle.Ref{Owner: "p", ID: "x"}))},
		Providers: []kind.Provider{{ID: "p", Version: "1", Builtins: []kind.AnyBuiltin{kind.Builtin(greeting, "x", "x")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.All(c, greeting)) != 2 {
		t.Errorf("All = %+v", bundle.All(c, greeting))
	}
}

func TestLoadReportsEveryBundlesProblems(t *testing.T) {
	_, err := load(t, bundle.Options{
		Sources: []bundle.Source{
			bundle.Dir("testdata/invalid/path-escape"),
			bundle.Dir("testdata/invalid/unknown-field"),
			bundle.Dir("testdata/invalid/asset-unreferenced"),
		},
		Compilers: testCompilers,
	})
	bundletest.RequireCode(t, err, bundle.CodePathForbidden)
	bundletest.RequireCode(t, err, bundle.CodeManifestInvalid)
	bundletest.RequireCode(t, err, bundle.CodeAssetUnreferenced)
}

func TestLoadIsDeterministic(t *testing.T) {
	opts := bundle.Options{
		Sources: []bundle.Source{
			bundle.Dir("testdata/invalid/path-escape"),
			bundle.Dir("testdata/invalid/duplicate-contribution"),
			bundle.Dir("testdata/invalid/asset-missing"),
			bundle.Embedded("m", hello("  - {id: a, kind: example.greeting@1, path: a}\n  - {id: b, kind: example.greeting@1, path: b}\n")),
		},
		Compilers: append([]bundle.AnyCompiler{bundle.Compile(greeting, compile.Text())}, testCompilers...),
	}
	_, first := load(t, opts)
	for range 10 {
		_, again := load(t, opts)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("reports differ:\n%v\n%v", first, again)
		}
	}
}

func TestLoadRejectsSymlinkInDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bundle.yaml", "apiVersion: craggy.io/bundle/v1\nmetadata: {id: a, version: '1'}\ncontributions: [{id: x, kind: example.script@1, path: link}]\n")
	outside := filepath.Join(t.TempDir(), "outside.star")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Dir(dir)}, Compilers: testCompilers})
	bundletest.RequireCode(t, err, bundle.CodeSymlinkForbidden)
}

func TestDirAssetChangedAfterLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/valid/example")); err != nil {
		t.Fatal(err)
	}
	c, err := load(t, bundle.Options{Sources: []bundle.Source{bundle.Dir(dir)}, Compilers: testCompilers})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := bundle.Get(c, prompt, bundle.Ref{Owner: "example.hello", ID: "review"})
	tone, _ := def.Asset("assets/style/tone.md")
	path := filepath.Join(dir, "assets", "style", "tone.md")
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tone.Read(context.Background()); !errors.Is(err, bundle.ErrAssetChanged) {
		t.Errorf("Read after change = %v, want ErrAssetChanged", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := tone.Read(context.Background()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read after removal = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := bundle.Load(ctx, bundle.Options{Sources: []bundle.Source{bundle.Embedded("m", hello())}})
	if c != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Load = %v, %v; want context.Canceled", c, err)
	}
}

type rejectAll struct{}

func (rejectAll) Validate(any) error { return errors.New("rejected") }

func refersTo(ref bundle.Ref) bundle.Compiler[string] {
	return func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
		return bundle.Compiled[string]{Value: "x", Refs: []bundle.Ref{ref}}, nil
	}
}

func merge(maps ...fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
