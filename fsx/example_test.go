package fsx_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/craggy-io/foundation-kit/fsx"
)

func Example() {
	dir, _ := os.MkdirTemp("", "fsx-example")
	defer os.RemoveAll(dir)

	// One process at a time owns the directory.
	lock, err := fsx.TryLock(filepath.Join(dir, "owner.lock"))
	if err != nil {
		fmt.Println("busy:", errors.Is(err, fsx.ErrLocked))
		return
	}
	defer lock.Unlock()

	// Readers see the old state or the new one, never half of each.
	state := filepath.Join(dir, "state.json")
	if err := fsx.WriteFile(state, []byte(`{"step":1}`), 0o644); err != nil {
		fmt.Println(err)
		return
	}
	b, _ := os.ReadFile(state)
	fmt.Println(string(b))
	// Output: {"step":1}
}
