package kind_test

import (
	"fmt"

	"github.com/craggy-io/foundation-kit/kind"
)

func ExampleParseID() {
	id, err := kind.ParseID("cclab.intel-pack@1")
	fmt.Println(id.Name, id.Version, err)

	_, err = kind.ParseID("script/1")
	fmt.Println(err)

	// Output:
	// cclab.intel-pack 1 <nil>
	// kind: invalid kind "script/1": want namespace.name@N
}

func ExampleBuiltin() {
	type Profile struct{ Retries int }

	profiles := kind.New[Profile]("example.profile", 1)

	// A library can export this without importing the bundle loader.
	defaults := kind.Provider{
		ID:      "example.defaults",
		Version: "1.0.0",
		Builtins: []kind.AnyBuiltin{
			kind.Builtin(profiles, "default", Profile{Retries: 3}),
		},
	}

	for _, b := range defaults.Builtins {
		fmt.Println(b.Kind().ID(), b.ID(), b.Value())
	}

	// Output:
	// example.profile@1 default {3}
}
