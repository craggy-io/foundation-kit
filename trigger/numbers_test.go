package trigger_test

import (
	"encoding/json"
	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/trigger"
	"testing"
)

func TestNumericMatchingAndKeysAreExact(t *testing.T) {
	spec := trigger.Spec{Event: "test.number@1", Where: trigger.Predicate{Op: "eq", Path: "data.n", Value: json.RawMessage(`9007199254740993`)}, Deduplicate: []string{"data.n"}}
	fact := event.Envelope{Type: spec.Event, Data: json.RawMessage(`{"n":9007199254740993.0}`)}
	_, match, err := spec.Match(fact)
	if err != nil || !match {
		t.Fatalf("equivalent decimal did not match: %v", err)
	}
	first, err := spec.Key("binding", fact)
	requireSuccess(t, err)
	fact.Data = json.RawMessage(`{"n":9007199254740993}`)
	second, err := spec.Key("binding", fact)
	requireSuccess(t, err)
	if first != second {
		t.Fatal("equivalent numbers produced distinct keys")
	}
	fact.Data = json.RawMessage(`{"n":9007199254740992}`)
	_, match, err = spec.Match(fact)
	if err != nil || match {
		t.Fatal("large integer lost precision")
	}
}
