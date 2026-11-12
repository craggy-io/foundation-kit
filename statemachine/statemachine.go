package statemachine

import (
	"context"
	"fmt"
	"slices"
)

// Step is one move through a machine, as passed to guards and actions.
type Step[S, E comparable] struct {
	Machine string // the machine's name
	From    S
	Event   E
	To      S
}

// Transition moves a machine from one state to another when an event is fired.
// A state has at most one transition per event.
type Transition[S, E comparable] struct {
	From S
	On   E
	To   S
	// Guard, when set, decides whether the transition may happen. An error
	// refuses it, and Fire returns that error wrapped with ErrRefused.
	Guard func(ctx context.Context, step Step[S, E]) error
	// Do, when set, is the work of the transition. It runs after Guard, and an
	// error from it leaves the state unchanged.
	Do func(ctx context.Context, step Step[S, E]) error
}

// Spec declares a machine. S and E are the consumer's own state and event types.
type Spec[S, E comparable] struct {
	Name        string
	States      []S // every state, each listed once
	Initial     S   // where a new Tracker starts
	Terminal    []S // states nothing leaves; may be empty for a machine that never finishes
	Transitions []Transition[S, E]
}

// Machine is a validated, immutable state machine. It holds no current state.
type Machine[S, E comparable] struct {
	name        string
	states      []S
	declared    map[S]bool
	terminal    map[S]bool
	initial     S
	transitions []Transition[S, E]
	byEdge      map[edge[S, E]]Transition[S, E]
}

type edge[S, E comparable] struct {
	from S
	on   E
}

// New validates spec and returns its machine. When the spec is inconsistent it
// returns an *Error holding every problem found, not just the first.
func New[S, E comparable](spec Spec[S, E]) (*Machine[S, E], error) {
	m := &Machine[S, E]{
		name:     spec.Name,
		declared: map[S]bool{},
		terminal: map[S]bool{},
		initial:  spec.Initial,
		byEdge:   map[edge[S, E]]Transition[S, E]{},
	}
	var diags []Diagnostic
	add := func(code Code, state, event any, format string, args ...any) {
		d := Diagnostic{Code: code, Message: fmt.Sprintf(format, args...)}
		if state != nil {
			d.State = fmt.Sprint(state)
		}
		if event != nil {
			d.Event = fmt.Sprint(event)
		}
		diags = append(diags, d)
	}

	if spec.Name == "" {
		add(CodeNameInvalid, nil, nil, "the machine has no name")
	}
	for _, s := range spec.States {
		if m.declared[s] {
			add(CodeStateDuplicate, s, nil, "state %v is listed twice", s)
			continue
		}
		m.declared[s] = true
		m.states = append(m.states, s)
	}
	if !m.declared[spec.Initial] {
		add(CodeStateUndeclared, spec.Initial, nil, "initial state %v isn't in States", spec.Initial)
	}
	for _, s := range spec.Terminal {
		if !m.declared[s] {
			add(CodeStateUndeclared, s, nil, "terminal state %v isn't in States", s)
			continue
		}
		m.terminal[s] = true
	}

	next, prev := map[S][]S{}, map[S][]S{}
	for _, t := range spec.Transitions {
		ends := []S{t.From}
		if t.To != t.From {
			ends = append(ends, t.To)
		}
		valid := true
		for _, s := range ends {
			if !m.declared[s] {
				add(CodeStateUndeclared, s, t.On, "the transition from %v on %v to %v uses state %v, which isn't in States", t.From, t.On, t.To, s)
				valid = false
			}
		}
		if !valid {
			continue
		}
		key := edge[S, E]{t.From, t.On}
		if _, dup := m.byEdge[key]; dup {
			add(CodeTransitionDuplicate, t.From, t.On, "state %v has two transitions for event %v", t.From, t.On)
			continue
		}
		if m.terminal[t.From] {
			add(CodeTerminalExit, t.From, t.On, "terminal state %v has a transition on %v to %v", t.From, t.On, t.To)
			continue
		}
		m.byEdge[key] = t
		m.transitions = append(m.transitions, t)
		next[t.From] = append(next[t.From], t.To)
		prev[t.To] = append(prev[t.To], t.From)
	}

	reachable := reach(next, spec.Initial)
	finishing := reach(prev, spec.Terminal...)
	for _, s := range m.states {
		if m.declared[spec.Initial] && !reachable[s] {
			add(CodeStateUnreachable, s, nil, "state %v can't be reached from initial state %v", s, spec.Initial)
		}
		switch {
		case m.terminal[s]:
		case len(next[s]) == 0:
			add(CodeStateDeadEnd, s, nil, "state %v has no transitions out and isn't terminal", s)
		case len(m.terminal) > 0 && !finishing[s]:
			add(CodeStateTrapped, s, nil, "state %v has no path to a terminal state", s)
		}
	}

	if len(diags) > 0 {
		return nil, &Error{Machine: spec.Name, Diagnostics: diags}
	}
	return m, nil
}

// reach returns every state that following edges from starts arrives at,
// including starts themselves.
func reach[S comparable](edges map[S][]S, starts ...S) map[S]bool {
	seen := map[S]bool{}
	queue := slices.Clone(starts)
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if seen[s] {
			continue
		}
		seen[s] = true
		queue = append(queue, edges[s]...)
	}
	return seen
}

// Name returns the machine's name.
func (m *Machine[S, E]) Name() string { return m.name }

// Initial returns the state a new Tracker starts in.
func (m *Machine[S, E]) Initial() S { return m.initial }

// States returns every state, in the order the Spec listed them.
func (m *Machine[S, E]) States() []S { return slices.Clone(m.states) }

// Has reports whether state is one of the machine's states.
func (m *Machine[S, E]) Has(state S) bool { return m.declared[state] }

// IsTerminal reports whether state is terminal.
func (m *Machine[S, E]) IsTerminal(state S) bool { return m.terminal[state] }

// Events returns the events that state has a transition for, in the order the
// Spec listed them. A guard may still refuse one.
func (m *Machine[S, E]) Events(state S) []E {
	var events []E
	for _, t := range m.transitions {
		if t.From == state {
			events = append(events, t.On)
		}
	}
	return events
}

// Next returns the state that event leads to from state, without running the
// guard or action. It returns ErrUnknownState or ErrNoTransition otherwise.
func (m *Machine[S, E]) Next(from S, event E) (S, error) {
	t, err := m.transition(from, event)
	if err != nil {
		return from, err
	}
	return t.To, nil
}

// Fire runs the transition for event from the given state: the guard, then the
// action. It returns the next state, or from and an error when there is no
// such transition, the guard refuses, or the action fails.
func (m *Machine[S, E]) Fire(ctx context.Context, from S, event E) (S, error) {
	t, err := m.transition(from, event)
	if err != nil {
		return from, err
	}
	step := Step[S, E]{Machine: m.name, From: from, Event: event, To: t.To}
	if t.Guard != nil {
		if err := t.Guard(ctx, step); err != nil {
			return from, fmt.Errorf("statemachine %q: %v on %v: %w: %w", m.name, from, event, ErrRefused, err)
		}
	}
	if t.Do != nil {
		if err := t.Do(ctx, step); err != nil {
			return from, fmt.Errorf("statemachine %q: %v on %v: %w", m.name, from, event, err)
		}
	}
	return t.To, nil
}

func (m *Machine[S, E]) transition(from S, event E) (Transition[S, E], error) {
	if !m.declared[from] {
		return Transition[S, E]{}, fmt.Errorf("statemachine %q: %w %v", m.name, ErrUnknownState, from)
	}
	t, ok := m.byEdge[edge[S, E]{from, event}]
	if !ok {
		return Transition[S, E]{}, fmt.Errorf("statemachine %q: %w from %v on %v", m.name, ErrNoTransition, from, event)
	}
	return t, nil
}
