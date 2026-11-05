package bundle

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle/internal/digest"
	"github.com/craggy-io/foundation-kit/kind"
)

const assetPath = "assets/model.bin"

// countingSource records how many times it has been opened and released.
type countingSource struct {
	fsys           fs.FS
	opened, closed int
}

func (s *countingSource) Origin() string { return "test" }

func (s *countingSource) Open(ctx context.Context) (fs.FS, func() error, error) {
	s.opened++
	return s.fsys, func() error { s.closed++; return nil }, nil
}

// newTestAsset returns an asset accepted with the given content, and the file system
// behind it so that a test can change the file afterwards.
func newTestAsset(t *testing.T, content string) (Asset, fstest.MapFS, *countingSource) {
	t.Helper()
	h := digest.New()
	h.Write([]byte(content))
	files := fstest.MapFS{assetPath: {Data: []byte(content)}}
	source := &countingSource{fsys: files}
	return Asset{source: source, path: assetPath, size: int64(len(content)), digest: digest.Sum(h)}, files, source
}

func TestAssetRead(t *testing.T) {
	a, _, source := newTestAsset(t, "hello")
	if source.opened != 0 {
		t.Fatalf("source opened %d times before any read", source.opened)
	}
	got, err := a.Read(context.Background())
	if err != nil || string(got) != "hello" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if source.opened != 1 || source.closed != 1 {
		t.Errorf("source opened %d and closed %d times, want 1 and 1", source.opened, source.closed)
	}
}

func TestAssetReadReturnsNoBytesWhenContentChanged(t *testing.T) {
	a, files, source := newTestAsset(t, "hello")
	files[assetPath].Data = []byte("HELLO") // same size, so only the digest can tell
	got, err := a.Read(context.Background())
	if !errors.Is(err, ErrAssetChanged) || got != nil {
		t.Errorf("Read = %q, %v, want no bytes and ErrAssetChanged", got, err)
	}
	if source.closed != 1 {
		t.Errorf("source closed %d times, want 1", source.closed)
	}
}

func TestAssetOpenReportsChangedSizeImmediately(t *testing.T) {
	a, files, source := newTestAsset(t, "hello")
	files[assetPath].Data = []byte("hello, world")
	if _, err := a.Open(context.Background()); !errors.Is(err, ErrAssetChanged) {
		t.Errorf("Open error = %v, want ErrAssetChanged", err)
	}
	if source.closed != 1 {
		t.Errorf("source closed %d times, want 1", source.closed)
	}
}

func TestAssetOpenReportsChangedContentAtEOF(t *testing.T) {
	a, files, _ := newTestAsset(t, "hello")
	files[assetPath].Data = []byte("HELLO")
	r, err := a.Open(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	// Unverified bytes do come through before EOF, as Open documents.
	if _, err := io.ReadFull(r, make([]byte, 2)); err != nil {
		t.Errorf("reading part of the stream: %v", err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrAssetChanged) {
		t.Errorf("reading to EOF: error = %v, want ErrAssetChanged", err)
	}
}

func TestAssetReaderStopsWhenFileGrowsAfterOpen(t *testing.T) {
	a, _, _ := newTestAsset(t, "hello")
	file, err := fstest.MapFS{assetPath: {Data: []byte("hello, world")}}.Open(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	r := &assetReader{ctx: context.Background(), asset: a, file: file, closeAll: file.Close, hash: digest.New()}
	defer r.Close()

	if _, err := io.ReadAll(r); !errors.Is(err, ErrAssetChanged) {
		t.Errorf("error = %v, want ErrAssetChanged", err)
	}
}

func TestAssetRemoved(t *testing.T) {
	a, files, source := newTestAsset(t, "hello")
	delete(files, assetPath)
	if _, err := a.Read(context.Background()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want fs.ErrNotExist", err)
	}
	if source.closed != 1 {
		t.Errorf("source closed %d times, want 1", source.closed)
	}
}

func TestAssetCancelled(t *testing.T) {
	a, _, source := newTestAsset(t, "hello")
	ctx, cancel := context.WithCancel(context.Background())

	r, err := a.Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	cancel()
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Errorf("Read after cancel: error = %v, want context.Canceled", err)
	}

	opened := source.opened
	if _, err := a.Open(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Open after cancel: error = %v, want context.Canceled", err)
	}
	if source.opened != opened {
		t.Error("Open reached the source with a cancelled context")
	}
}

func TestGetReturnsAssets(t *testing.T) {
	greeting := kind.New[string]("example.greeting", 1)
	a, _, _ := newTestAsset(t, "hello")
	ref := Ref{Owner: "b", ID: "hello"}
	c := &Catalog{
		order: []Ref{ref},
		definitions: map[Ref]storedDefinition{
			ref: {kind: greeting.ID(), value: "hi", assets: []Asset{a}},
		},
	}

	def, ok := Get(c, greeting, ref)
	if !ok {
		t.Fatalf("Get(%v) found nothing", ref)
	}
	found, ok := def.Asset(assetPath)
	if !ok || found.Path() != assetPath || found.Size() != 5 || found.Digest() != a.Digest() {
		t.Errorf("Asset(%q) = %+v, %v", assetPath, found, ok)
	}
	if _, ok := def.Asset("assets/other"); ok {
		t.Error("Asset found a path the definition does not list")
	}

	// Changing a returned slice must not reach the immutable catalog.
	def.Assets[0] = Asset{}
	again, _ := Get(c, greeting, ref)
	if again.Assets[0].Path() != assetPath {
		t.Error("changing a returned Assets slice changed the catalog")
	}
}

func TestContributionAsset(t *testing.T) {
	a, _, _ := newTestAsset(t, "hello")
	c := Contribution{Assets: []Asset{a}}
	if found, ok := c.Asset(assetPath); !ok || found.Path() != assetPath {
		t.Errorf("Asset(%q) = %+v, %v", assetPath, found, ok)
	}
	if _, ok := c.Asset("assets/other"); ok {
		t.Error("Asset found a path the contribution does not list")
	}
}
