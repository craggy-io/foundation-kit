package bundle_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/compile"
	"github.com/craggy-io/foundation-kit/kind"
)

var note = kind.New[string]("example.note", 1)

const noteManifest = `apiVersion: craggy.io/bundle/v1
metadata: {id: example.source, version: 1.0.0}
contributions:
  - id: readme
    kind: example.note@1
    path: notes/readme.txt
`

// sourceBundle is a bundle that ships a program's source as support files.
func sourceBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "bundle.yaml", noteManifest)
	write(t, dir, "notes/readme.txt", "hello")
	write(t, dir, "src/main.go", "package main\n")
	write(t, dir, "src/internal/deep.go", "package internal\n")
	return dir
}

func loadSource(t *testing.T, src bundle.Source) *bundle.Catalog {
	t.Helper()
	cat, err := bundle.Load(context.Background(), bundle.Options{
		Sources:   []bundle.Source{src},
		Compilers: []bundle.AnyCompiler{bundle.Compile(note, compile.Text())},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func bundleDigest(t *testing.T, cat *bundle.Catalog) string {
	t.Helper()
	owners := cat.Owners()
	if len(owners) != 1 || owners[0].Digest == "" {
		t.Fatalf("owners = %+v", owners)
	}
	return owners[0].Digest
}

func listFiles(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// Catalog.Files gives the bytes Load accepted, whatever has happened to the
// folder since.
func TestCatalogFilesAreTheAcceptedBytes(t *testing.T) {
	dir := sourceBundle(t)
	cat := loadSource(t, bundle.Dir(dir))

	files, ok := cat.Files("example.source")
	if !ok {
		t.Fatal("no files for the bundle")
	}
	want := []string{"bundle.yaml", "notes/readme.txt", "src/internal/deep.go", "src/main.go"}
	if got := listFiles(t, files); !slices.Equal(got, want) {
		t.Fatalf("files = %v", got)
	}

	// Change and delete source on disk after the load.
	write(t, dir, "src/main.go", "package tampered\n")
	if err := os.Remove(filepath.Join(dir, "src", "internal", "deep.go")); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"src/main.go":          "package main\n",
		"src/internal/deep.go": "package internal\n",
	} {
		got, err := fs.ReadFile(files, path)
		if err != nil || string(got) != content {
			t.Errorf("%s = %q, %v", path, got, err)
		}
	}

	if _, ok := cat.Files("no.such.bundle"); ok {
		t.Error("files returned for an unknown owner")
	}
}

// A provider has no files.
func TestCatalogFilesOfAProvider(t *testing.T) {
	cat, err := bundle.Load(context.Background(), bundle.Options{
		Providers: []kind.Provider{{ID: "example.defaults", Version: "1.0.0",
			Builtins: []kind.AnyBuiltin{kind.Builtin(note, "default", "hi")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Files("example.defaults"); ok {
		t.Error("files returned for a provider")
	}
}

// What tools leave in a source folder is not part of the bundle: it doesn't
// change the digest, isn't in Files, and may hold things a bundle can't, such
// as symlinks and more files than the limit allows.
func TestIgnoredFilesAreNotPartOfTheBundle(t *testing.T) {
	dir := sourceBundle(t)
	clean := loadSource(t, bundle.Dir(dir))

	for _, junk := range []string{
		".git/HEAD",
		"src/__pycache__/main.cpython-312.pyc",
		"src/node_modules/left-pad/index.js",
		"src/.venv/pyvenv.cfg",
		".vscode/settings.json",
		"src/.DS_Store",
		"assets/Thumbs.db", // unlisted, so an error if it counted as an asset
	} {
		write(t, dir, junk, "left behind by a tool")
	}
	dirty := loadSource(t, bundle.Dir(dir))

	if bundleDigest(t, dirty) != bundleDigest(t, clean) {
		t.Error("ignored files changed the bundle digest")
	}
	files, _ := dirty.Files("example.source")
	want := []string{"bundle.yaml", "notes/readme.txt", "src/internal/deep.go", "src/main.go"}
	if got := listFiles(t, files); !slices.Equal(got, want) {
		t.Errorf("files = %v", got)
	}
	if _, err := fs.Stat(files, "src/node_modules/left-pad/index.js"); err == nil {
		t.Error("an ignored file is readable")
	}

	// A symlink is forbidden in a bundle, but one inside an ignored folder is
	// never looked at.
	virtualenv := fstest.MapFS{
		"bundle.yaml":             {Data: []byte(noteManifest)},
		"notes/readme.txt":        {Data: []byte("hello")},
		"src/.venv/bin/python":    {Data: []byte("/usr/bin/python3"), Mode: fs.ModeSymlink},
		"src/.venv/lib/site.py":   {Data: []byte("x")},
		"src/node_modules/a/b.js": {Data: []byte("x")},
	}
	loadSource(t, bundle.Embedded("example", virtualenv))
}

// Install copies only what is part of the bundle.
func TestInstallLeavesIgnoredFilesBehind(t *testing.T) {
	ctx := context.Background()
	src := sourceBundle(t)
	write(t, src, ".git/HEAD", "ref: refs/heads/main")
	write(t, src, "src/__pycache__/main.pyc", "bytecode")

	installed := filepath.Join(t.TempDir(), "installed")
	live, err := bundle.NewLive(ctx, func(ctx context.Context) (bundle.Options, error) {
		children, err := bundle.Children(ctx, installed)
		return bundle.Options{
			Sources:   children,
			Compilers: []bundle.AnyCompiler{bundle.Compile(note, compile.Text())},
		}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := live.Install(ctx, installed, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(installed, "example.source")
	if _, err := os.Stat(filepath.Join(final, "src", "main.go")); err != nil {
		t.Error(err)
	}
	for _, gone := range []string{".git", filepath.Join("src", "__pycache__")} {
		if _, err := os.Stat(filepath.Join(final, gone)); !os.IsNotExist(err) {
			t.Errorf("%s was installed (%v)", gone, err)
		}
	}
}
