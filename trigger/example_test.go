package trigger_test

import (
	"encoding/json"
	"fmt"
	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/trigger"
)

func ExampleSpec_Match() {
	spec := trigger.Spec{Event: "files.created@1", Input: map[string]trigger.Mapping{"file": {Path: "data.name"}}}
	input, matched, err := spec.Match(event.Envelope{Type: "files.created@1", Data: json.RawMessage(`{"name":"report.txt"}`)})
	fmt.Println(string(input), matched, err)
	// Output: {"file":"report.txt"} true <nil>
}
