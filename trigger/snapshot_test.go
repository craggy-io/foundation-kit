package trigger_test

import (
	"encoding/json"
	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/trigger"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotsDoNotAliasJournal(t *testing.T) {
	j, err := trigger.Open(t.Context(), filepath.Join(t.TempDir(), "journal.json"))
	requireSuccess(t, err)
	defer j.Close()
	b := trigger.Binding{ID: "binding", Target: "work", Spec: trigger.Spec{Event: "test.created@1", Input: map[string]trigger.Mapping{"x": {Path: "data.x"}}}}
	requireSuccess(t, j.Replace(t.Context(), []trigger.Binding{b}))
	requireSuccess(t, j.Publish(t.Context(), event.Envelope{ID: "one", Source: "test", Type: b.Spec.Event, Time: time.Now().UTC(), Data: json.RawMessage(`{"x":"hello"}`)}))
	rows := j.List("")
	rows[0].Input[0] = '!'
	rows[0].Event.Data[0] = '!'
	bindings := j.Bindings("")
	bindings[0].Spec.Input["x"] = trigger.Mapping{Path: "data.absent"}
	fresh := j.List("")
	if !json.Valid(fresh[0].Input) || !json.Valid(fresh[0].Event.Data) {
		t.Fatal("snapshot mutated journal payload")
	}
	if j.Bindings("")[0].Spec.Input["x"].Path != "data.x" {
		t.Fatal("snapshot mutated binding")
	}
}
