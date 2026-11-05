package snapshot

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle/internal/digest"
)

func take(t *testing.T, fsys fs.FS, limits Limits) (*Snapshot, []Diagnostic) {
	t.Helper()
	return Take(context.Background(), fsys, limits)
}

func requireCode(t *testing.T, diags []Diagnostic, code string) {
	t.Helper()
	for _, d := range diags {
		if d.Code == code {
			return
		}
	}
	t.Fatalf("want %s, got %+v", code, diags)
}

func TestTake(t *testing.T) {
	snap, diags := take(t, fstest.MapFS{
		"bundle.yaml":      {Data: []byte("m")},
		"scripts/a.star":   {Data: []byte("a")},
		"assets/big.bin":   {Data: []byte("hello")},
		"assets/x/y/z.txt": {Data: []byte("")},
	}, Limits{})
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if len(snap.Files()) != 2 || string(snap.Files()["scripts/a.star"]) != "a" {
		t.Errorf("Files = %v", snap.Files())
	}
	h := digest.New()
	h.Write([]byte("hello"))
	if got := snap.Assets()["assets/big.bin"]; got.Size != 5 || got.Digest != digest.Sum(h) {
		t.Errorf("asset = %+v", got)
	}
	if _, ok := snap.Files()["assets/big.bin"]; ok {
		t.Error("an asset's bytes are in the snapshot")
	}
}

func TestTakeRejectsNestedManifest(t *testing.T) {
	for _, p := range []string{"sub/bundle.yaml", "assets/bundle.yaml"} {
		_, diags := take(t, fstest.MapFS{"bundle.yaml": {}, p: {}}, Limits{})
		requireCode(t, diags, codeManifestNested)
	}
}

func TestTakeRejectsSymlinks(t *testing.T) {
	for _, p := range []string{"link", "assets/link"} {
		_, diags := take(t, fstest.MapFS{"bundle.yaml": {}, p: {Data: []byte("bundle.yaml"), Mode: fs.ModeSymlink}}, Limits{})
		requireCode(t, diags, codeSymlinkForbidden)
	}
}

func TestTakeRejectsSymlinksOnDisk(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, diags := take(t, root.FS(), Limits{})
	requireCode(t, diags, codeSymlinkForbidden)
}

func TestTakeRejectsIrregularFiles(t *testing.T) {
	_, diags := take(t, fstest.MapFS{"bundle.yaml": {}, "pipe": {Mode: fs.ModeNamedPipe}}, Limits{})
	requireCode(t, diags, codePathForbidden)
}

func TestTakeLimits(t *testing.T) {
	files := fstest.MapFS{
		"bundle.yaml":   {Data: []byte("1234")},
		"a":             {Data: []byte("1234")},
		"assets/a.bin":  {Data: []byte("123456789")},
		"assets/b.bin":  {Data: []byte("1")},
		"assets/c.bin":  {Data: []byte("1")},
		"assets/d.bin":  {Data: []byte("1")},
		"assets/e.bin":  {Data: []byte("1")},
		"assets/f.bin":  {Data: []byte("1")},
		"assets/g.bin":  {Data: []byte("1")},
		"assets/h.bin":  {Data: []byte("1")},
		"assets/i.bin":  {Data: []byte("1")},
		"assets/j.bin":  {Data: []byte("1")},
		"assets/k.bin":  {Data: []byte("1")},
		"assets/l.bin":  {Data: []byte("1")},
		"assets/m.bin":  {Data: []byte("1")},
		"assets/n.bin":  {Data: []byte("1")},
		"assets/o.bin":  {Data: []byte("1")},
		"assets/p.bin":  {Data: []byte("1")},
		"assets/q.bin":  {Data: []byte("1")},
		"assets/rr.bin": {Data: []byte("1")},
	}
	if _, diags := take(t, files, Limits{MaxFiles: 100, MaxFileBytes: 4, MaxTotalBytes: 8}); len(diags) != 0 {
		t.Errorf("within limits: %+v", diags)
	}
	for name, l := range map[string]Limits{
		"files":       {MaxFiles: 10},
		"file bytes":  {MaxFileBytes: 3},
		"total bytes": {MaxTotalBytes: 7},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := take(t, files, l)
			requireCode(t, diags, codeLimitExceeded)
		})
	}
}

func TestTakeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snap, diags := Take(ctx, fstest.MapFS{"bundle.yaml": {}}, Limits{})
	if snap != nil || len(diags) == 0 {
		t.Errorf("Take with a cancelled context = %v, %+v", snap, diags)
	}
}

func TestFS(t *testing.T) {
	snap, diags := take(t, fstest.MapFS{
		"bundle.yaml":         {Data: []byte("m")},
		"scripts/a.star":      {Data: []byte("a")},
		"scripts/lib/b.star":  {Data: []byte("bb")},
		"schemas/x/y/z.json":  {Data: []byte("{}")},
		"assets/not-here.bin": {Data: []byte("x")},
	}, Limits{})
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	fsys := snap.FS()
	if err := fstest.TestFS(fsys, "bundle.yaml", "scripts/a.star", "scripts/lib/b.star", "schemas/x/y/z.json"); err != nil {
		t.Error(err)
	}
	if _, err := fs.Stat(fsys, "assets/not-here.bin"); err == nil {
		t.Error("the snapshot's FS contains an asset")
	}

	// Reads return copies, so changing one can't change the snapshot.
	data, err := fs.ReadFile(fsys, "scripts/a.star")
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if again, _ := fs.ReadFile(fsys, "scripts/a.star"); string(again) != "a" {
		t.Error("changing bytes from ReadFile changed the snapshot")
	}
}
