package bundle_test

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/compile"
)

// A bundle declaring data_schemas loads with no compiler for them: the
// declarations ride the catalog beside Files, and no definition or
// contribution exists for a schema.
func TestCatalogDataSchemas(t *testing.T) {
	files := fstest.MapFS{
		"bundle.yaml": {Data: []byte(`apiVersion: craggy.io/bundle/v1
metadata: {id: example.lib, version: 1.0.0}
data_schemas:
  - type: example.library@1
    path: schemas/example.library.json
    producer: {template: example-dataset, from: [cwe], cadence: "168h"}
contributions:
  - id: readme
    kind: example.note@1
    path: notes/readme.txt
`)},
		"schemas/example.library.json": {Data: []byte(`{"type":"example.library@1"}`)},
		"notes/readme.txt":             {Data: []byte("hello")},
	}
	cat, err := bundle.Load(context.Background(), bundle.Options{
		Sources:   []bundle.Source{bundle.Embedded("test", files)},
		Compilers: []bundle.AnyCompiler{bundle.Compile(note, compile.Text())},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cat.DataSchemas("example.lib")
	if !ok || len(got) != 1 {
		t.Fatalf("DataSchemas = %+v, %v", got, ok)
	}
	d := got[0]
	if d.Type != "example.library@1" || d.Path != "schemas/example.library.json" || d.TitleSet ||
		d.Producer == nil || d.Producer.Template != "example-dataset" || d.Producer.Cadence != "168h" {
		t.Errorf("schema = %+v producer %+v", d, d.Producer)
	}
	// The schema file is one of the bundle's accepted files, under its digest.
	bundleFiles, ok := cat.Files("example.lib")
	if !ok {
		t.Fatal("no files")
	}
	if _, err := fs.ReadFile(bundleFiles, d.Path); err != nil {
		t.Errorf("reading the schema file through Files: %v", err)
	}
	if _, ok := cat.DataSchemas("example.unknown"); ok {
		t.Errorf("an unknown owner reported schemas")
	}
	if defs := bundle.All(cat, note); len(defs) != 1 {
		t.Errorf("definitions = %d, want only the note (a schema is not one)", len(defs))
	}
}

// A bundle with no data_schemas reports an empty list, not an unknown owner.
func TestCatalogDataSchemasNone(t *testing.T) {
	cat := loadSource(t, bundle.Dir(sourceBundle(t)))
	got, ok := cat.DataSchemas("example.source")
	if !ok || len(got) != 0 {
		t.Errorf("DataSchemas = %+v, %v", got, ok)
	}
}
