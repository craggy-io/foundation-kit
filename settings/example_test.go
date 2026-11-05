package settings_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/craggy-io/foundation-kit/settings"
)

func Example() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "settings-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := settings.New(ctx, settings.File(filepath.Join(dir, "settings.json")))
	if err != nil {
		log.Fatal(err)
	}

	// The code that reads a setting declares it, and keeps the Setting.
	maxBytes := settings.Int(store, settings.IntSpec[int64]{
		Meta: settings.Meta{
			Key:         "search.strings.max-bytes",
			Title:       "Largest file to search",
			Description: "Larger files are skipped.",
			Group:       "Search",
		},
		Default: 1 << 20,
		Min:     1,
		Max:     1 << 30,
		Unit:    settings.UnitBytes,
	})
	fmt.Println(maxBytes.Get())

	// A settings UI lists settings, and sends changes back as JSON.
	if _, err := store.Set(ctx, "search.strings.max-bytes", json.RawMessage(`2048`)); err != nil {
		log.Fatal(err)
	}
	fmt.Println(maxBytes.Get())

	_, err = store.Set(ctx, "search.strings.max-bytes", json.RawMessage(`0`))
	fmt.Println(err)

	// Output:
	// 1048576
	// 2048
	// settings: invalid value: search.strings.max-bytes takes 1 to 1073741824, got 0
}
