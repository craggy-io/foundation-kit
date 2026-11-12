package statemachine

import (
	"context"
	"fmt"
	"sync"
)

// Tracker holds the current state of one thing moving through a Machine. It is
// safe for concurrent use and runs one Fire at a time.
type Tracker[S, E comparable] struct {
	machine *Machine[S, E]
	firing  sync.Mutex // held for a whole Fire, callbacks included
	mu      sync.Mutex // guards state
	state   S
}

// Start returns a Tracker in the machine's initial state.
func (m *Machine[S, E]) Start() *Tracker[S, E] {
	return &Tracker[S, E]{machine: m, state: m.initial}
}

// Resume returns a Tracker in a state that was stored earlier. It returns
// ErrUnknownState when the machine no longer has that state.
func (m *Machine[S, E]) Resume(state S) (*Tracker[S, E], error) {
	if !m.declared[state] {
		return nil, fmt.Errorf("statemachine %q: %w %v", m.name, ErrUnknownState, state)
	}
	return &Tracker[S, E]{machine: m, state: state}, nil
}

// State returns the current state. It is safe to call from a guard or action,
// where it still returns the state being left.
func (t *Tracker[S, E]) State() S {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Done reports whether the current state is terminal.
func (t *Tracker[S, E]) Done() bool { return t.machine.IsTerminal(t.State()) }

// Fire runs the transition for event from the current state and, when it
// succeeds, moves to the next state. It returns the state the Tracker is in
// afterwards. On an error the state is unchanged.
//
// The context passed to the guard and action records that this Tracker is
// firing, so a callback that fires at the same Tracker with it gets
// ErrReentrant.
func (t *Tracker[S, E]) Fire(ctx context.Context, event E) (S, error) {
	for f, _ := ctx.Value(firingKey{}).(*firing); f != nil; f = f.parent {
		if f.tracker == any(t) {
			return t.State(), fmt.Errorf("statemachine %q: %w", t.machine.name, ErrReentrant)
		}
	}
	t.firing.Lock()
	defer t.firing.Unlock()

	parent, _ := ctx.Value(firingKey{}).(*firing)
	ctx = context.WithValue(ctx, firingKey{}, &firing{tracker: t, parent: parent})
	next, err := t.machine.Fire(ctx, t.State(), event)
	if err != nil {
		return next, err
	}
	t.mu.Lock()
	t.state = next
	t.mu.Unlock()
	return next, nil
}

// firing is the chain of Trackers whose callbacks the current call is inside.
type firing struct {
	tracker any
	parent  *firing
}

type firingKey struct{}
