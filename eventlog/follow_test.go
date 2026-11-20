package eventlog_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/craggy-io/foundation-kit/eventlog"
)

func TestTail(t *testing.T) {
	e := newEnv(t)
	e.writer.MaxSegments = 2
	for i := 1; i <= 5; i++ {
		e.append(ev(fmt.Sprint("e", i), "market.bundle.published@1", "b", `{}`))
	}
	e.append(ev("d", "data.dataset.released@1", "d", `{}`))
	records, cur, err := e.reader.Tail(context.Background(), 3)
	if err != nil || ids(records) != "e4,e5,d" || cur.Seq != 6 {
		t.Fatalf("Tail(3) = %q, %+v, %v", ids(records), cur, err)
	}
	e.reader.Filter = eventlog.Filter{Types: []string{"market"}}
	if records, _, _ = e.reader.Tail(context.Background(), 10); ids(records) != "e1,e2,e3,e4,e5" {
		t.Fatalf("filtered Tail = %q", ids(records))
	}
}

func TestFollowerSavesTheCursorOnlyAfterHandling(t *testing.T) {
	e := newEnv(t)
	e.append(ev("a", "market.bundle.published@1", "b1", `{}`))
	cursor := eventlog.FileCursor(filepath.Join(t.TempDir(), "cursor.json"))
	var seen []string
	fail := true
	f := &eventlog.Follower{Reader: e.reader, Cursor: cursor, Handle: func(_ context.Context, b eventlog.Batch) error {
		if fail {
			return errors.New("busy")
		}
		seen = append(seen, fmt.Sprintf("%v:%s", b.Reset, ids(b.Records)))
		return nil
	}}
	ctx := context.Background()
	if _, err := f.Poll(ctx); err == nil || f.Status().Failures != 1 || f.Status().LastError == "" {
		t.Fatalf("a failed handle: %v, %+v", err, f.Status())
	}
	if c, _ := cursor.Load(); c != (eventlog.Cursor{}) {
		t.Fatalf("the cursor moved although handling failed: %+v", c)
	}
	fail = false
	if _, err := f.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	e.append(ev("b", "market.bundle.published@1", "b2", `{}`))

	// A new follower over the same file (a restarted harness) carries on.
	g := &eventlog.Follower{Reader: e.reader, Cursor: cursor, Handle: f.Handle}
	if _, err := g.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(seen) != "[true:a false:b]" {
		t.Fatalf("batches = %v", seen)
	}
	if s := g.Status(); s.Failures != 0 || s.Cursor.Seq != 2 || s.Records != 1 || s.LastSuccess.IsZero() {
		t.Fatalf("status = %+v", s)
	}
}

func TestFollowerRunPollsOnPoke(t *testing.T) {
	e := newEnv(t)
	got := make(chan eventlog.Batch, 4)
	f := &eventlog.Follower{Reader: e.reader, Every: time.Hour, MinEvery: time.Millisecond, Handle: func(_ context.Context, b eventlog.Batch) error {
		got <- b
		return nil
	}}
	e.append(ev("a", "market.bundle.published@1", "b", `{}`))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	if b := <-got; !b.Reset || ids(b.Records) != "a" {
		t.Fatalf("first batch = %q", ids(b.Records))
	}
	e.append(ev("b", "market.bundle.published@1", "b", `{}`))
	f.Poke()
	select {
	case b := <-got:
		if ids(b.Records) != "b" {
			t.Fatalf("poked batch = %q", ids(b.Records))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Poke didn't poll")
	}
	if next := f.Status().Next; next.Before(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("next poll at %s, want about an hour away", next)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}

func TestFollowerNeverPollsSoonerThanMinEvery(t *testing.T) {
	e := newEnv(t)
	const gap = 300 * time.Millisecond
	polls := make(chan time.Time, 16)
	f := &eventlog.Follower{Reader: e.reader, Every: time.Millisecond, MinEvery: gap, Handle: func(context.Context, eventlog.Batch) error {
		polls <- time.Now()
		return nil
	}}
	e.append(ev("a", "market.bundle.published@1", "b", `{}`))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	last := <-polls
	for range 3 {
		f.Poke() // a poke straight after a poll waits for the gap
		next := <-polls
		if d := next.Sub(last); d < gap-10*time.Millisecond {
			t.Fatalf("polls %s apart, want at least %s", d, gap)
		}
		last = next
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}
