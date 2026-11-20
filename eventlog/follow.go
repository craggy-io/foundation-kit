package eventlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/craggy-io/foundation-kit/fsx"
)

// CursorStore keeps a follower's cursor between runs.
type CursorStore interface {
	// Load returns the saved cursor, or the zero Cursor when there is none.
	Load() (Cursor, error)
	Save(Cursor) error
}

// FileCursor keeps a cursor in a JSON file, replaced atomically.
type FileCursor string

// Load implements CursorStore.
func (f FileCursor) Load() (Cursor, error) {
	data, err := os.ReadFile(string(f))
	if errors.Is(err, fs.ErrNotExist) {
		return Cursor{}, nil
	}
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return Cursor{}, fmt.Errorf("eventlog: the cursor in %s can't be read: %w", string(f), err)
	}
	return c, nil
}

// Save implements CursorStore.
func (f FileCursor) Save(c Cursor) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return fsx.WriteFile(string(f), data, 0o644)
}

// memoryCursor is the store a Follower uses when given none.
type memoryCursor struct{ c Cursor }

func (m *memoryCursor) Load() (Cursor, error) { return m.c, nil }
func (m *memoryCursor) Save(c Cursor) error   { m.c = c; return nil }

// Defaults for a Follower's zero fields.
const (
	defaultEvery      = 2 * time.Minute
	defaultMaxBackoff = 30 * time.Minute
)

// Follower polls a log on a timer and hands each batch to Handle, saving the
// cursor only after Handle succeeds, so a batch that fails is offered again.
// Run it in one goroutine; Poll, Poke and Status are safe from any.
type Follower struct {
	Reader *Reader
	// Cursor keeps the cursor between runs (FileCursor in a state folder).
	// Nil keeps it in memory, so each run starts with a reset.
	Cursor CursorStore
	// Every is the time between polls (default 2 minutes). A random extra of
	// up to a fifth of it spreads many readers over time.
	Every time.Duration
	// MaxBackoff caps the wait after failures, which doubles from Every
	// (default 30 minutes).
	MaxBackoff time.Duration
	// Handle receives every batch, including empty ones. An error leaves the
	// cursor where it was.
	Handle func(context.Context, Batch) error
	// OnError, when set, is told of every failed poll.
	OnError func(error)

	mu     sync.Mutex // serialises polls: a Reader is single-goroutine
	smu    sync.Mutex
	status Status
	poke   chan struct{}
	once   sync.Once
}

// Status is what a follower has done, for showing to a person.
type Status struct {
	Cursor      Cursor    `json:"cursor"`
	LastPoll    time.Time `json:"last_poll,omitzero"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	// Failures counts polls failed in a row.
	Failures int `json:"failures"`
	// Next is when Run polls next.
	Next time.Time `json:"next,omitzero"`
	// Records counts the records handed to Handle since the follower began.
	Records int `json:"records"`
}

func (f *Follower) init() {
	f.once.Do(func() {
		f.poke = make(chan struct{}, 1)
		if f.Cursor == nil {
			f.Cursor = &memoryCursor{}
		}
	})
}

// Status returns what the follower has done so far.
func (f *Follower) Status() Status {
	f.smu.Lock()
	defer f.smu.Unlock()
	return f.status
}

func (f *Follower) update(change func(*Status)) {
	f.smu.Lock()
	defer f.smu.Unlock()
	change(&f.status)
}

// Poke asks Run to poll now instead of waiting for its timer.
func (f *Follower) Poke() {
	f.init()
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

// Poll reads the log once, hands the batch to Handle and saves the cursor.
func (f *Follower) Poll(ctx context.Context) (Batch, error) {
	f.init()
	f.mu.Lock()
	defer f.mu.Unlock()
	batch, err := f.poll(ctx)
	now := time.Now()
	f.update(func(s *Status) {
		s.LastPoll = now
		if err != nil {
			s.LastError, s.Failures = err.Error(), s.Failures+1
			return
		}
		s.LastSuccess, s.LastError, s.Failures = now, "", 0
		s.Cursor, s.Records = batch.Cursor, s.Records+len(batch.Records)
	})
	if err != nil && f.OnError != nil {
		f.OnError(err)
	}
	return batch, err
}

func (f *Follower) poll(ctx context.Context) (Batch, error) {
	cur, err := f.Cursor.Load()
	if err != nil {
		return Batch{}, err
	}
	batch, err := f.Reader.Next(ctx, cur)
	if err != nil {
		return Batch{}, err
	}
	if f.Handle != nil {
		if err := f.Handle(ctx, batch); err != nil {
			return Batch{}, fmt.Errorf("eventlog: handling a batch: %w", err)
		}
	}
	if batch.Cursor != cur {
		if err := f.Cursor.Save(batch.Cursor); err != nil {
			return Batch{}, fmt.Errorf("eventlog: saving the cursor: %w", err)
		}
	}
	return batch, nil
}

// Run polls now and then on its timer until ctx ends, which it returns.
func (f *Follower) Run(ctx context.Context) error {
	f.init()
	every := or(f.Every, defaultEvery)
	for {
		_, err := f.Poll(ctx)
		wait := every + rand.N(every/5+1)
		if err != nil {
			wait = min(every<<min(f.Status().Failures, 16), or(f.MaxBackoff, defaultMaxBackoff))
		}
		f.update(func(s *Status) { s.Next = time.Now().Add(wait) })
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-f.poke:
			timer.Stop()
		case <-timer.C:
		}
	}
}
