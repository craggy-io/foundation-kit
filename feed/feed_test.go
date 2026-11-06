package feed

import (
	"context"
	"testing"
	"time"
)

func recv[T any](t *testing.T, ch <-chan Entry[T]) Entry[T] {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return e
	case <-time.After(time.Second):
		t.Fatal("no entry")
	}
	return Entry[T]{}
}

func TestPublishDeliversInOrder(t *testing.T) {
	h := New[string](Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, complete := h.Subscribe(ctx, -1)
	if !complete {
		t.Fatal("fresh subscription should be complete")
	}
	h.Publish("a")
	h.Publish("b")
	if e := recv(t, ch); e.Seq != 1 || e.Value != "a" || e.Time.IsZero() {
		t.Fatalf("first = %+v", e)
	}
	if e := recv(t, ch); e.Seq != 2 || e.Value != "b" {
		t.Fatalf("second = %+v", e)
	}
}

func TestReplayAndGaps(t *testing.T) {
	h := New[int](Options{Backlog: 2})
	for i := range 4 {
		h.Publish(i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, complete := h.Subscribe(ctx, 2)
	if !complete {
		t.Fatal("replay within backlog should be complete")
	}
	if e := recv(t, ch); e.Seq != 3 {
		t.Fatalf("replay starts at %d, want 3", e.Seq)
	}
	if _, complete := h.Subscribe(ctx, 1); complete {
		t.Fatal("gap older than backlog should be incomplete")
	}
	if _, complete := h.Subscribe(ctx, 9); complete {
		t.Fatal("cursor ahead of hub (restart) should be incomplete")
	}
	if h.Seq() != 4 {
		t.Fatalf("Seq = %d", h.Seq())
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	h := New[int](Options{Buffer: 1})
	ch, _ := h.Subscribe(context.Background(), -1)
	h.Publish(1)
	h.Publish(2) // buffer full: dropped
	<-ch
	if _, ok := <-ch; ok {
		t.Fatal("slow subscriber should be closed")
	}
}

func TestCancelClosesChannel(t *testing.T) {
	h := New[int](Options{})
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := h.Subscribe(ctx, -1)
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("not closed after cancel")
	}
}
