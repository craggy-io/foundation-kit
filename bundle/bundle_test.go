package bundle

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/kind"
)

// These tests cover the small parts of the package that are already implemented.

func TestParseRef(t *testing.T) {
	tests := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "a.b#c", want: Ref{Owner: "a.b", ID: "c"}},
		{in: "", wantErr: true},
		{in: "a.b", wantErr: true},
		{in: "#c", wantErr: true},
		{in: "a.b#", wantErr: true},
		{in: "a#b#c", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseRef(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("ParseRef(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestReadsOnlyDefinitionsOfTheGivenKind(t *testing.T) {
	greeting := kind.New[string]("example.greeting", 1)
	counter := kind.New[int]("example.counter", 1)

	hello := Ref{Owner: "b", ID: "hello"}
	one := Ref{Owner: "b", ID: "one"}
	c := &Catalog{
		order: []Ref{hello, one},
		definitions: map[Ref]storedDefinition{
			hello: {kind: greeting.ID(), value: "hi"},
			one:   {kind: counter.ID(), value: 1},
		},
	}

	defs := All(c, greeting)
	if len(defs) != 1 || defs[0].Ref != hello || defs[0].Value != "hi" {
		t.Errorf("All(greeting) = %+v, want only %v", defs, hello)
	}
	if _, ok := Get(c, greeting, one); ok {
		t.Errorf("Get(greeting, %v) succeeded for a definition of another kind", one)
	}
}

func TestCompilePairsAKindWithItsCompiler(t *testing.T) {
	greeting := kind.New[string]("example.greeting", 1)
	ref := Ref{Owner: "b", ID: "dep"}
	c := Compile(greeting, func(ctx context.Context, c Contribution) (Compiled[string], []Diagnostic) {
		return Compiled[string]{Value: "hi", Refs: []Ref{ref}}, nil
	})

	if c.Kind().ID() != greeting.ID() {
		t.Errorf("Kind().ID() = %v, want %v", c.Kind().ID(), greeting.ID())
	}
	out, diags := c.compile(context.Background(), Contribution{})
	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v, want none", diags)
	}
	if out.value != "hi" || len(out.refs) != 1 || out.refs[0] != ref {
		t.Errorf("compile = %+v, want the compiler's value and refs", out)
	}
}

func TestEmbeddedOpenAndClose(t *testing.T) {
	want := fstest.MapFS{"bundle.yaml": {Data: []byte("x")}}
	fsys, closeFn, err := Embedded("test", want).Open(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fsys.Open("bundle.yaml"); err != nil {
		t.Errorf("reading through the opened file system: %v", err)
	}
	if err := closeFn(); err != nil {
		t.Errorf("closeFn: %v", err)
	}
}
