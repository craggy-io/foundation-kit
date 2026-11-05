package bundle_test

import (
	"context"
	"fmt"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/compile"
	"github.com/craggy-io/foundation-kit/kind"
)

// This example has no expected-output comment, so go test compiles it without
// running it.
//
// TODO: add the expected output once Load is implemented.
func Example() {
	// A kind is declared once, by whoever owns that type of definition.
	greeting := kind.New[string]("example.greeting", 1)

	// bundle.yaml sits at the root of the bundle.
	files := fstest.MapFS{
		"bundle.yaml": {Data: []byte(`
apiVersion: craggy.io/bundle/v1
metadata:
  id: example.hello
  version: 0.1.0
contributions:
  - id: hello
    kind: example.greeting@1
    path: greetings/hello.txt
`)},
		"greetings/hello.txt": {Data: []byte("hello")},
	}

	catalog, err := bundle.Load(context.Background(), bundle.Options{
		// Definitions from a bundle need a compiler for their kind.
		Sources:   []bundle.Source{bundle.Embedded("example", files)},
		Compilers: []bundle.AnyCompiler{bundle.Compile(greeting, compile.Text())},

		// Definitions from Go code join the same catalog.
		Providers: []kind.Provider{{
			ID:       "example.defaults",
			Version:  "1.0.0",
			Builtins: []kind.AnyBuiltin{kind.Builtin(greeting, "default", "hi")},
		}},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	// Definitions come back typed, with no casting.
	for _, def := range bundle.All(catalog, greeting) {
		fmt.Println(def.Ref, def.Value)
	}
}
