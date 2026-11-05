package bundle_test

import (
	"context"
	"fmt"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/compile"
	"github.com/craggy-io/foundation-kit/kind"
)

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

	// Output:
	// example.defaults#default hi
	// example.hello#hello hello
}

func ExampleDir() {
	script := kind.New[string]("example.script", 1)
	prompt := kind.New[string]("example.prompt", 1)

	catalog, err := bundle.Load(context.Background(), bundle.Options{
		Sources: []bundle.Source{bundle.Dir("testdata/valid/example")},
		Compilers: []bundle.AnyCompiler{
			bundle.Compile(script, compile.Text()),
			bundle.Compile(prompt, compile.Text()),
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	review, _ := bundle.Get(catalog, prompt, bundle.Ref{Owner: "example.hello", ID: "review"})
	fmt.Print(review.Value)

	// Assets are read on demand, and checked against the digest Load accepted.
	tone, _ := review.Asset("assets/style/tone.md")
	data, err := tone.Read(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(string(data))

	// Output:
	// Review the submitted change and list any problems.
	// Be direct, and explain the reason for each problem.
}
