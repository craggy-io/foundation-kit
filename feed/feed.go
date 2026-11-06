package feed

import (
	"context"
	"sync"
	"time"
)

// Entry is one published value with its sequence and publish time.
type Entry[T any] struct {
	Seq   int64     `json:"seq"`
	Time  time.Time `json:"time"`
	Value T         `json:"value"`
}

// Options sizes a Hub. Zero values pick the defaults.
type Options struct {
	// Backlog is how many recent entries a reconnecting subscriber can catch up
	// on. Default 1024.
	Backlog int
	// Buffer is each subscriber's queue. A subscriber this far behind is
	// dropped. Default 256.
	Buffer int
}

// Hub fans entries out to subscribers. Build one with New.
type Hub[T any] struct {
	backlog, buffer int

	mu   sync.Mutex
	seq  int64
	ring []Entry[T] // the last backlog entries, oldest first
	subs map[chan Entry[T]]struct{}
}

// New returns an empty hub.
func New[T any](opts Options) *Hub[T] {
	if opts.Backlog <= 0 {
		opts.Backlog = 1024
	}
	if opts.Buffer <= 0 {
		opts.Buffer = 256
	}
	return &Hub[T]{backlog: opts.Backlog, buffer: opts.Buffer, subs: map[chan Entry[T]]struct{}{}}
}

// Publish stamps v with the next sequence and the current time, records it and
// delivers it to every subscriber without blocking. It returns the entry.
func (h *Hub[T]) Publish(v T) Entry[T] {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	e := Entry[T]{Seq: h.seq, Time: time.Now().UTC(), Value: v}
	if len(h.ring) == h.backlog {
		copy(h.ring, h.ring[1:])
		h.ring = h.ring[:h.backlog-1]
	}
	h.ring = append(h.ring, e)
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
	return e
}

// Seq is the last published sequence, 0 before the first entry.
func (h *Hub[T]) Seq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// Subscribe returns a channel of every entry after sequence after, starting with
// the backlog already published. complete is false when the entries after after
// are no longer all in the backlog, or after is ahead of the hub; the caller must
// then reload everything rather than trust the replay. A negative after
// subscribes from now: no replay, and complete is true. The channel closes when
// ctx ends or the subscriber falls too far behind.
func (h *Hub[T]) Subscribe(ctx context.Context, after int64) (entries <-chan Entry[T], complete bool) {
	h.mu.Lock()
	if after < 0 {
		after = h.seq
	}
	oldest := h.seq + 1
	if len(h.ring) > 0 {
		oldest = h.ring[0].Seq
	}
	complete = after <= h.seq && after >= oldest-1
	var replay []Entry[T]
	for _, e := range h.ring {
		if e.Seq > after {
			replay = append(replay, e)
		}
	}
	ch := make(chan Entry[T], h.buffer+len(replay))
	for _, e := range replay {
		ch <- e
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	context.AfterFunc(ctx, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	})
	return ch, complete
}
