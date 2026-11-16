package trigger_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/trigger"
)

func TestDurableAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	j, err := trigger.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	e := event.Envelope{ID: "upload-1", Type: "files.created@1", Source: "files", Scope: "one", Time: time.Now().UTC(), Data: json.RawMessage(`{"id":"a","name":"a.SO","digest":"abc"}`)}
	spec := trigger.Spec{Event: e.Type, Where: trigger.Predicate{Op: "ends_with", Path: "data.name", Value: json.RawMessage(`".so"`)}, Input: map[string]trigger.Mapping{"subject": {Path: "data.id"}}, Deduplicate: []string{"data.id", "data.digest"}}
	if err := j.Replace(t.Context(), []trigger.Binding{{ID: "inspect", Target: "analyse", Scope: "one", Spec: spec}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := j.Publish(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	rows := j.List("one")
	if len(rows) != 1 || string(rows[0].Input) != `{"subject":"a"}` {
		t.Fatalf("admissions: %+v", rows)
	}
	if len(j.List("two")) != 0 {
		t.Fatal("scope leaked")
	}
	d := rows[0]
	j = recoverAdmission(t, j, path, d)
	defer j.Close()
	e.ID = "upload-2"
	if err := j.Publish(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(j.List("one")) != 1 {
		t.Fatal("business deduplication lost")
	}
	e.ID = "upload-3"
	e.Data = json.RawMessage(`{"id":"a","name":"a.so","digest":"changed"}`)
	if err := j.Publish(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(j.List("one")) != 2 {
		t.Fatal("new content not admitted")
	}
}

func TestReplayFailuresAndCausation(t *testing.T) {
	j, err := trigger.Open(t.Context(), filepath.Join(t.TempDir(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	e := event.Envelope{ID: "1", Source: "test", Type: "test.event@1", Time: time.Now().UTC(), Data: json.RawMessage(`{}`)}
	if err := j.Publish(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	b := trigger.Binding{ID: "binding", Target: "target", Spec: trigger.Spec{Event: e.Type, Manual: true}}
	if err := j.Replace(t.Context(), []trigger.Binding{b}); err != nil {
		t.Fatal(err)
	}
	if len(j.List("")) != 0 {
		t.Fatal("unexpected history replay")
	}
	b.Spec.Replay = true
	if err := j.Replace(t.Context(), []trigger.Binding{b}); err != nil {
		t.Fatal(err)
	}
	if j.List("")[0].Status != "held" {
		t.Fatal("manual release ignored")
	}
	e.ID = "2"
	e.Causation = []string{b.ID}
	if err := j.Publish(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(j.List("")) != 1 {
		t.Fatal("causation cycle admitted")
	}
	b.Spec.Input = map[string]trigger.Mapping{"missing": {Path: "data.absent"}}
	if err := j.Replace(t.Context(), []trigger.Binding{b}); err != nil {
		t.Fatal(err)
	}
	e.ID = "3"
	e.Causation = nil
	if err := j.Publish(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	rows := j.List("")
	if rows[len(rows)-1].Status != "failed" {
		t.Fatal("missing mapping silently accepted")
	}
}

func recoverAdmission(t *testing.T, j *trigger.Journal, path string, d trigger.Delivery) *trigger.Journal {
	t.Helper()
	if err := j.Transition(t.Context(), "one", d.ID, "ready", "running", "", false); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := trigger.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	recovered := j.List("one")[0]
	if recovered.Status != "interrupted" || recovered.RunID() != d.RunID() {
		t.Fatalf("recovery: %+v", recovered)
	}
	return j
}
