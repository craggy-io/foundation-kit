package feed_test

import (
	"context"
	"fmt"

	"github.com/craggy-io/foundation-kit/feed"
)

func Example() {
	h := feed.New[string](feed.Options{})
	h.Publish("labs.create")
	h.Publish("labs.rename")

	// A reader that last saw sequence 1 reconnects and gets what it missed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, complete := h.Subscribe(ctx, 1)
	e := <-entries
	fmt.Println(complete, e.Seq, e.Value)
	// Output: true 2 labs.rename
}
