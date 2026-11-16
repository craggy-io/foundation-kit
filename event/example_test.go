package event_test

import (
	"encoding/json"
	"fmt"
	"github.com/craggy-io/foundation-kit/event"
	"time"
)

func ExampleEnvelope_Validate() {
	fact := event.Envelope{ID: "upload-1", Type: "files.created@1", Source: "files", Time: time.Unix(1, 0), Data: json.RawMessage(`{"name":"report.txt"}`)}
	fmt.Println(fact.Validate())
	// Output: <nil>
}
