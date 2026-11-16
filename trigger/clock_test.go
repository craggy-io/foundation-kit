package trigger_test

import (
	"github.com/craggy-io/foundation-kit/trigger"
	"path/filepath"
	"testing"
	"time"
)

func TestClockCoalescesAndRemovesDeadline(t *testing.T) {
	j, err := trigger.Open(t.Context(), filepath.Join(t.TempDir(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	b := trigger.Binding{ID: "tick", Target: "work", Spec: trigger.Spec{Event: "clock.tick@1", Clock: &trigger.Source{Kind: "every", Interval: time.Minute}}}
	requireSuccess(t, j.Replace(t.Context(), []trigger.Binding{b}))
	now := time.Now().UTC().Add(24 * time.Hour)
	requireSuccess(t, j.Tick(t.Context(), now))
	requireSuccess(t, j.Tick(t.Context(), now))
	rows := j.List("")
	if len(rows) != 1 {
		t.Fatalf("missed ticks did not coalesce: %d", len(rows))
	}
	requireSuccess(t, j.Replace(t.Context(), nil))
	requireSuccess(t, j.Tick(t.Context(), now.Add(time.Hour)))
	if len(j.List("")) != 1 {
		t.Fatal("removed timer fired")
	}
	if err := j.Transition(t.Context(), "", rows[0].ID, "ready", "running", "", false); err == nil {
		t.Fatal("removed binding claimed")
	}
	if err := j.Transition(t.Context(), "", rows[0].ID, "ready", "done", "", false); err == nil {
		t.Fatal("invalid transition accepted")
	}
}

func requireSuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
