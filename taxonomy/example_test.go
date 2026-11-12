package taxonomy_test

import (
	"fmt"

	"github.com/craggy-io/foundation-kit/taxonomy"
)

// A host keeps one Live. Each owner declares its own types, building on the
// types other owners declared.
func Example() {
	live := taxonomy.NewLive()

	// The host declares the base types.
	live.Declare("host", []taxonomy.Type{
		{Name: "tree", Description: "A folder and everything under it"},
		{Name: "source-tree", Parents: []string{"tree"}, Description: "A tree of source code"},
	})

	// Later, a Java extension adds its own, at run time.
	live.Declare("java-extension", []taxonomy.Type{
		{Name: "source-tree.java", Parents: []string{"source-tree"}},
	})

	t := live.Taxonomy()
	fmt.Println(t.IsA("source-tree.java", "tree"))
	fmt.Println(t.Ancestors("source-tree.java"))
	fmt.Println(t.Descendants("tree"))

	// The host can't withdraw types the extension still builds on.
	_, err := live.Withdraw("host")
	fmt.Println(err != nil)

	// Output:
	// true
	// [source-tree tree]
	// [source-tree source-tree.java]
	// true
}
