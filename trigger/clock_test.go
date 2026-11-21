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

// A cron source with a zone fires at its wall-clock time there, across a
// change of offset.
func TestCronInZone(t *testing.T) {
	s := trigger.Source{Kind: "cron", Expr: "0 2 * * *", TZ: "Europe/London"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	winter := s.Next(time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC))
	summer := s.Next(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	if winter != time.Date(2026, 1, 11, 2, 0, 0, 0, time.UTC) || summer != time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC) {
		t.Fatalf("winter %v, summer %v", winter, summer)
	}
	if (trigger.Source{Kind: "cron", Expr: "0 2 * * *", TZ: "Nowhere/City"}).Validate() == nil {
		t.Fatal("an unknown zone was taken")
	}
}
