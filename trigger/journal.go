package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/craggy-io/foundation-kit/canonjson"
	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/fsx"
)

// Binding connects a trigger to an opaque target in one host-established scope.
type Binding struct {
	Revision string `json:"revision,omitempty"`
	ID       string `json:"id"`
	Target   string `json:"target"`
	Scope    string `json:"scope"`
	Spec     Spec   `json:"spec"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Delivery pins the event, input and target admitted together. Running work is
// restored as interrupted; hosts must resume with the same RunID, never replay
// an uncertain side effect as a fresh attempt automatically.
type Delivery struct {
	Revision string          `json:"revision,omitempty"`
	ID       string          `json:"id"`
	Binding  string          `json:"binding"`
	Target   string          `json:"target"`
	Scope    string          `json:"scope"`
	Event    event.Envelope  `json:"event"`
	Input    json.RawMessage `json:"input"`
	Status   string          `json:"status"`
	Attempt  int             `json:"attempt"`
	Error    string          `json:"error,omitempty"`
	Created  time.Time       `json:"created"`
}

// RunID remains stable across process restarts and changes only on explicit retry.
func (d Delivery) RunID() string { return fmt.Sprintf("event-%s-%d", d.ID, d.Attempt) }

type journalState struct {
	Next       map[string]time.Time `json:"next"`
	Events     []event.Envelope     `json:"events"`
	Bindings   map[string]Binding   `json:"bindings"`
	Deliveries map[string]Delivery  `json:"deliveries"`
}

// Journal owns one durable admission journal, protected by an exclusive file
// lock. Mutations are copy-on-write: failed persistence never changes live state.
type Journal struct {
	mu     sync.Mutex
	path   string
	lock   *fsx.Lock
	state  journalState
	closed bool
}

// Open acquires the journal and recovers running admissions as interrupted.
func Open(ctx context.Context, path string) (*Journal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := fsx.TryLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	j := &Journal{path: path, lock: lock, state: journalState{Bindings: map[string]Binding{}, Deliveries: map[string]Delivery{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &j.state)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		lock.Unlock()
		return nil, err
	}
	if j.state.Bindings == nil || j.state.Deliveries == nil {
		lock.Unlock()
		return nil, fmt.Errorf("trigger: invalid journal")
	}
	if j.state.Next == nil {
		j.state.Next = map[string]time.Time{}
	}
	for id, d := range j.state.Deliveries {
		if d.Status == "running" {
			d.Status = "interrupted"
			j.state.Deliveries[id] = d
		}
	}
	return j, nil
}

// Close releases the journal after all dispatch workers have stopped.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.lock.Unlock()
}

func (j *Journal) mutate(ctx context.Context, fn func(*journalState) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return os.ErrClosed
	}
	b, err := json.Marshal(j.state)
	if err != nil {
		return err
	}
	var next journalState
	if err := json.Unmarshal(b, &next); err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	b, err = json.Marshal(next)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fsx.WriteFile(j.path, b, 0600); err != nil {
		return err
	}
	var committed journalState
	if err := json.Unmarshal(b, &committed); err != nil {
		return err
	}
	j.state = committed
	return nil
}

// Replace atomically activates the accepted binding set. History is replayed
// only for explicitly replayable bindings. Existing deliveries keep their pins.
func (j *Journal) Replace(ctx context.Context, bindings []Binding) error {
	j.mu.Lock()
	if j.closed {
		j.mu.Unlock()
		return os.ErrClosed
	}
	same := len(bindings) == len(j.state.Bindings)
	for _, b := range bindings {
		old, ok := j.state.Bindings[b.ID]
		if !ok || !reflect.DeepEqual(old, b) {
			same = false
			break
		}
	}
	j.mu.Unlock()
	if same {
		return nil
	}
	return j.mutate(ctx, func(s *journalState) error {
		next := map[string]Binding{}
		deadlines := map[string]time.Time{}
		for _, b := range bindings {
			if b.ID == "" || b.Target == "" {
				return fmt.Errorf("trigger: binding needs id and target")
			}
			if _, exists := next[b.ID]; exists {
				return fmt.Errorf("trigger: duplicate binding %s", b.ID)
			}
			if err := b.Spec.Validate(); err != nil {
				return err
			}
			next[b.ID] = b
			if b.Spec.Clock != nil {
				deadlines[b.ID] = s.Next[b.ID]
				if deadlines[b.ID].IsZero() || !reflect.DeepEqual(s.Bindings[b.ID].Spec.Clock, b.Spec.Clock) {
					deadlines[b.ID] = b.Spec.Clock.Next(time.Now().UTC())
				}
			}
			if b.Spec.Replay {
				for _, e := range s.Events {
					admit(s, b, e)
				}
			}
		}
		s.Bindings = next
		s.Next = deadlines
		return nil
	})
}

// Publish persists a fact and its admissions in the same atomic write. A retry
// of the same source/scope/id must have identical content.
func (j *Journal) Publish(ctx context.Context, e event.Envelope) error {
	data, err := canonjson.Normalize(e.Data)
	if err != nil {
		return err
	}
	e.Data = data
	if err := e.Validate(); err != nil {
		return err
	}
	return j.mutate(ctx, func(s *journalState) error {
		for _, old := range s.Events {
			if old.ID != e.ID || old.Source != e.Source || old.Scope != e.Scope {
				continue
			}
			old.Causation = e.Causation // host-derived metadata may differ on an acknowledged retry
			if !equalJSON(old, e) {
				return fmt.Errorf("trigger: event identity reused with different content")
			}
			return nil
		}
		s.Events = append(s.Events, e)
		for _, b := range s.Bindings {
			admit(s, b, e)
		}
		return nil
	})
}

func admit(s *journalState, b Binding, e event.Envelope) {
	if b.Spec.Clock != nil && e.Subject != b.ID {
		return
	}
	if b.Disabled || b.Scope != e.Scope || slices.Contains(e.Causation, b.ID) || len(e.Causation) >= 16 {
		return
	}
	input, match, err := b.Spec.Match(e)
	if !match && err == nil {
		return
	}
	id, keyErr := b.Spec.Key(b.ID, e)
	if keyErr != nil {
		id, _ = (Spec{}).Key(b.ID, e)
		err = keyErr
	}
	if _, exists := s.Deliveries[id]; exists {
		return
	}
	d := Delivery{Revision: b.Revision, ID: id, Binding: b.ID, Target: b.Target, Scope: b.Scope, Event: e, Input: input, Status: "ready", Attempt: 1, Created: time.Now().UTC()}
	if b.Spec.Manual {
		d.Status = "held"
	}
	if err != nil {
		d.Status = "failed"
		d.Error = err.Error()
	}
	s.Deliveries[id] = d
}

// Tick persists due timer occurrences and their next deadline atomically.
// Missed intervals coalesce into one occurrence after restart; cron is UTC.
func (j *Journal) Tick(ctx context.Context, now time.Time) error {
	j.mu.Lock()
	if j.closed {
		j.mu.Unlock()
		return os.ErrClosed
	}
	due := false
	for id, at := range j.state.Next {
		b, ok := j.state.Bindings[id]
		if ok && !b.Disabled && !at.IsZero() && !at.After(now) {
			due = true
			break
		}
	}
	j.mu.Unlock()
	if !due {
		return nil
	}
	return j.mutate(ctx, func(s *journalState) error {
		for id, at := range s.Next {
			b, ok := s.Bindings[id]
			if !ok || b.Disabled || b.Spec.Clock == nil || at.After(now) || at.IsZero() {
				continue
			}
			e := event.Envelope{ID: id + "/" + at.Format(time.RFC3339Nano), Type: "clock.tick@1", Source: "clock", Scope: b.Scope, Subject: id, Time: at, Data: json.RawMessage(`{}`)}
			s.Events = append(s.Events, e)
			admit(s, b, e)
			s.Next[id] = b.Spec.Clock.Next(now)
		}
		return nil
	})
}

// Bindings returns detached active bindings for one scope.
func (j *Journal) Bindings(scope string) []Binding {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []Binding{}
	for _, b := range j.state.Bindings {
		if b.Scope == scope {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b Binding) int { return strings.Compare(a.ID, b.ID) })
	raw, _ := json.Marshal(out)
	var detached []Binding
	_ = json.Unmarshal(raw, &detached)
	return detached
}

// List returns detached admissions for exactly one scope, oldest first.
func (j *Journal) List(scope string) []Delivery {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []Delivery{}
	for _, d := range j.state.Deliveries {
		if d.Scope == scope {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Delivery) int { return a.Created.Compare(b.Created) })
	b, _ := json.Marshal(out)
	var detached []Delivery
	_ = json.Unmarshal(b, &detached)
	return detached
}

// Transition is a compare-and-swap state change. A claim additionally checks
// that its binding is still active. Retry is explicit and changes the run ID.
func (j *Journal) Transition(ctx context.Context, scope, id, from, to, message string, retry bool) error {
	if !allowedTransition(from, to, retry) {
		return fmt.Errorf("trigger: invalid transition %s -> %s", from, to)
	}
	return j.mutate(ctx, func(s *journalState) error {
		d, ok := s.Deliveries[id]
		if !ok || d.Scope != scope {
			return fmt.Errorf("trigger: delivery not found")
		}
		if d.Status != from {
			return fmt.Errorf("trigger: delivery state changed")
		}
		if to == "running" {
			b, ok := s.Bindings[d.Binding]
			if !ok || b.Disabled || b.Target != d.Target || b.Revision != d.Revision {
				return fmt.Errorf("trigger: binding is inactive")
			}
		}
		d.Status, d.Error = to, message
		if retry {
			b, ok := s.Bindings[d.Binding]
			if !ok || b.Target != d.Target {
				return fmt.Errorf("trigger: original binding target is unavailable")
			}
			d.Revision = b.Revision
			d.Attempt++
		}
		s.Deliveries[id] = d
		return nil
	})
}

func allowedTransition(from, to string, retry bool) bool {
	if retry {
		return to == "ready" && (from == "failed" || from == "cancelled" || from == "interrupted")
	}
	switch from {
	case "held":
		return to == "ready"
	case "ready", "interrupted":
		return to == "running" || to == "failed"
	case "running", "waiting":
		return to == "done" || to == "waiting" || to == "failed" || to == "cancelled" || to == "interrupted"
	}
	return false
}
