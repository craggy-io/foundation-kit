package eventlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"time"

	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/eventlog"
	"github.com/craggy-io/foundation-kit/eventlog/eventlogtest"
)

func Example() {
	ctx := context.Background()
	// The writer's store. In production, a bucket that the public reads.
	store := &eventlogtest.MemStore{}
	server := httptest.NewServer(store)
	defer server.Close()

	w := &eventlog.Writer{Store: store, Signer: eventlogtest.Signer("root-1")}
	_, _ = w.Append(ctx, []event.Envelope{{
		ID: "01", Type: "market.bundle.published@1", Source: "market", Scope: "public",
		Subject: "acme.tools", Time: time.Now(), Data: json.RawMessage(`{"version":"1.2.0"}`),
	}})

	// A reader trusts the root key and follows the log with a cursor it keeps.
	r := &eventlog.Reader{
		URL:    server.URL + "/",
		Roots:  []eventlog.Key{eventlogtest.Key("root-1").Key()},
		Filter: eventlog.Filter{Types: []string{"market.bundle"}},
	}
	batch, err := r.Next(ctx, eventlog.Cursor{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, rec := range batch.Records {
		fmt.Println(batch.Reset, rec.Seq, rec.Event.Type, rec.Event.Subject)
	}
	// Output: true 1 market.bundle.published@1 acme.tools
}
