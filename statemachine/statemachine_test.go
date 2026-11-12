package statemachine

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

type state string
type event string

const (
	pending state = "pending"
	running state = "running"
	done    state = "done"
	failed  state = "failed"

	start  event = "start"
	finish event = "finish"
	fail   event = "fail"
	retry  event = "retry"
)

type transitions = []Transition[state, event]

func jobSpec() Spec[state, event] {
	return Spec[state, event]{
		Name:     "job",
		States:   []state{pending, running, done, failed},
		Initial:  pending,
		Terminal: []state{done, failed},
		Transitions: transitions{
			{From: pending, On: start, To: running},
			{From: running, On: finish, To: done},
			{From: running, On: fail, To: failed},
			{From: running, On: retry, To: running},
		},
	}
}

func mustNew(t *testing.T, spec Spec[state, event]) *Machine[state, event] {
	t.Helper()
	m, err := New(spec)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestNewAcceptsConsistentSpec(t *testing.T) {
	m := mustNew(t, jobSpec())
	if m.Name() != "job" || m.Initial() != pending {
		t.Errorf("Name, Initial = %q, %v", m.Name(), m.Initial())
	}
	if got := m.States(); !slices.Equal(got, []state{pending, running, done, failed}) {
		t.Errorf("States = %v", got)
	}
	if got := m.Events(running); !slices.Equal(got, []event{finish, fail, retry}) {
		t.Errorf("Events(running) = %v", got)
	}
	if !m.IsTerminal(done) || m.IsTerminal(running) || !m.Has(pending) || m.Has("nope") {
		t.Error("IsTerminal or Has is wrong")
	}
}

func TestNewAcceptsMachineWithoutTerminalStates(t *testing.T) {
	mustNew(t, Spec[state, event]{
		Name:    "light",
		States:  []state{"red", "green"},
		Initial: "red",
		Transitions: transitions{
			{From: "red", On: "tick", To: "green"},
			{From: "green", On: "tick", To: "red"},
		},
	})
}

func TestNewReportsEachProblem(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Spec[state, event])
		want   Code
		state  string
	}{
		{"no name", func(s *Spec[state, event]) { s.Name = "" }, CodeNameInvalid, ""},
		{"state listed twice", func(s *Spec[state, event]) { s.States = append(s.States, running) }, CodeStateDuplicate, "running"},
		{"initial undeclared", func(s *Spec[state, event]) { s.Initial = "nope" }, CodeStateUndeclared, "nope"},
		{"terminal undeclared", func(s *Spec[state, event]) { s.Terminal = append(s.Terminal, "nope") }, CodeStateUndeclared, "nope"},
		{"transition target undeclared", func(s *Spec[state, event]) {
			s.Transitions = append(s.Transitions, Transition[state, event]{From: pending, On: fail, To: "nope"})
		}, CodeStateUndeclared, "nope"},
		{"two transitions for one event", func(s *Spec[state, event]) {
			s.Transitions = append(s.Transitions, Transition[state, event]{From: pending, On: start, To: failed})
		}, CodeTransitionDuplicate, "pending"},
		{"transition out of terminal", func(s *Spec[state, event]) {
			s.Transitions = append(s.Transitions, Transition[state, event]{From: done, On: retry, To: running})
		}, CodeTerminalExit, "done"},
		{"unreachable state", func(s *Spec[state, event]) {
			s.States = append(s.States, "orphan")
			s.Transitions = append(s.Transitions, Transition[state, event]{From: "orphan", On: finish, To: done})
		}, CodeStateUnreachable, "orphan"},
		{"dead end", func(s *Spec[state, event]) {
			s.States = append(s.States, "stuck")
			s.Transitions = append(s.Transitions, Transition[state, event]{From: pending, On: fail, To: "stuck"})
		}, CodeStateDeadEnd, "stuck"},
		{"trapped in a loop", func(s *Spec[state, event]) {
			s.States = append(s.States, "spin")
			s.Transitions = append(s.Transitions,
				Transition[state, event]{From: pending, On: fail, To: "spin"},
				Transition[state, event]{From: "spin", On: retry, To: "spin"})
		}, CodeStateTrapped, "spin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := jobSpec()
			tt.change(&spec)
			_, err := New(spec)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("New error = %v, want *Error", err)
			}
			if len(e.Diagnostics) != 1 {
				t.Fatalf("got %d diagnostics, want 1: %v", len(e.Diagnostics), e)
			}
			if d := e.Diagnostics[0]; d.Code != tt.want || d.State != tt.state {
				t.Errorf("diagnostic = %s for state %q, want %s for %q", d.Code, d.State, tt.want, tt.state)
			}
			if !e.Has(tt.want) {
				t.Errorf("Has(%s) = false", tt.want)
			}
		})
	}
}

func TestNewReportsEveryProblemAtOnce(t *testing.T) {
	spec := jobSpec()
	spec.Name = ""
	spec.States = append(spec.States, "orphan", "orphan")
	_, err := New(spec)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("New error = %v, want *Error", err)
	}
	for _, code := range []Code{CodeNameInvalid, CodeStateDuplicate, CodeStateUnreachable, CodeStateDeadEnd} {
		if !e.Has(code) {
			t.Errorf("missing %s in %v", code, e)
		}
	}
}

func TestNewCopiesTheSpec(t *testing.T) {
	spec := jobSpec()
	m := mustNew(t, spec)
	spec.States[0] = "changed"
	spec.Transitions[0].To = failed
	m.States()[1] = "changed"
	if got, err := m.Next(pending, start); err != nil || got != running {
		t.Errorf("Next after changing the spec = %v, %v", got, err)
	}
	if !m.Has(running) || m.Has("changed") {
		t.Error("the machine changed with its spec")
	}
}

func TestNextAndFireErrors(t *testing.T) {
	m := mustNew(t, jobSpec())
	ctx := context.Background()

	if got, err := m.Next(pending, finish); !errors.Is(err, ErrNoTransition) || got != pending {
		t.Errorf("Next(pending, finish) = %v, %v", got, err)
	}
	if got, err := m.Fire(ctx, done, retry); !errors.Is(err, ErrNoTransition) || got != done {
		t.Errorf("Fire from terminal = %v, %v", got, err)
	}
	if _, err := m.Fire(ctx, "nope", start); !errors.Is(err, ErrUnknownState) {
		t.Errorf("Fire from unknown state error = %v", err)
	}
	if _, err := m.Resume("nope"); !errors.Is(err, ErrUnknownState) {
		t.Errorf("Resume unknown state error = %v", err)
	}
}

func TestFireRunsGuardThenAction(t *testing.T) {
	var calls []string
	var seen Step[state, event]
	spec := jobSpec()
	spec.Transitions[0].Guard = func(_ context.Context, s Step[state, event]) error {
		calls = append(calls, "guard")
		return nil
	}
	spec.Transitions[0].Do = func(_ context.Context, s Step[state, event]) error {
		calls = append(calls, "do")
		seen = s
		return nil
	}
	m := mustNew(t, spec)

	got, err := m.Fire(context.Background(), pending, start)
	if err != nil || got != running {
		t.Fatalf("Fire = %v, %v", got, err)
	}
	if !slices.Equal(calls, []string{"guard", "do"}) {
		t.Errorf("calls = %v", calls)
	}
	if want := (Step[state, event]{Machine: "job", From: pending, Event: start, To: running}); seen != want {
		t.Errorf("step = %+v, want %+v", seen, want)
	}
}

func TestFailedCallbackLeavesStateUnchanged(t *testing.T) {
	errNoWorkspace := errors.New("no workspace")
	errDisk := errors.New("disk full")
	acted := false
	spec := jobSpec()
	spec.Transitions[0].Guard = func(context.Context, Step[state, event]) error { return errNoWorkspace }
	spec.Transitions[0].Do = func(context.Context, Step[state, event]) error { acted = true; return nil }
	spec.Transitions[1].Do = func(context.Context, Step[state, event]) error { return errDisk }
	m := mustNew(t, spec)
	ctx := context.Background()

	tr := m.Start()
	got, err := tr.Fire(ctx, start)
	if !errors.Is(err, ErrRefused) || !errors.Is(err, errNoWorkspace) {
		t.Errorf("refused Fire error = %v", err)
	}
	if got != pending || tr.State() != pending || acted {
		t.Errorf("after refusal: returned %v, state %v, acted %v", got, tr.State(), acted)
	}

	tr, err = m.Resume(running)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Fire(ctx, finish); !errors.Is(err, errDisk) || errors.Is(err, ErrRefused) {
		t.Errorf("failed action error = %v", err)
	}
	if tr.State() != running || tr.Done() {
		t.Errorf("after failed action: state %v", tr.State())
	}
}

func TestTrackerMovesToTerminal(t *testing.T) {
	m := mustNew(t, jobSpec())
	ctx := context.Background()
	tr := m.Start()
	for _, e := range []event{start, retry, finish} {
		if _, err := tr.Fire(ctx, e); err != nil {
			t.Fatalf("Fire(%v): %v", e, err)
		}
	}
	if tr.State() != done || !tr.Done() {
		t.Errorf("state = %v, done = %v", tr.State(), tr.Done())
	}
	if _, err := tr.Fire(ctx, start); !errors.Is(err, ErrNoTransition) {
		t.Errorf("Fire at terminal error = %v", err)
	}
}

func TestTrackerRejectsReentrantFire(t *testing.T) {
	var tr *Tracker[state, event]
	var inner error
	var during state
	spec := jobSpec()
	spec.Transitions[0].Do = func(ctx context.Context, _ Step[state, event]) error {
		during = tr.State()
		_, inner = tr.Fire(ctx, finish)
		return nil
	}
	tr = mustNew(t, spec).Start()

	if _, err := tr.Fire(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inner, ErrReentrant) {
		t.Errorf("reentrant Fire error = %v", inner)
	}
	if during != pending || tr.State() != running {
		t.Errorf("state during callback %v, after %v", during, tr.State())
	}
}

func TestCallbackMayFireAnotherTracker(t *testing.T) {
	inner := mustNew(t, jobSpec()).Start()
	spec := jobSpec()
	spec.Transitions[0].Do = func(ctx context.Context, _ Step[state, event]) error {
		_, err := inner.Fire(ctx, start)
		return err
	}
	outer := mustNew(t, spec).Start()
	if _, err := outer.Fire(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	if inner.State() != running || outer.State() != running {
		t.Errorf("inner %v, outer %v", inner.State(), outer.State())
	}
}

func TestTrackerSerialisesConcurrentFire(t *testing.T) {
	m := mustNew(t, jobSpec())
	tr := m.Start()
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tr.Fire(ctx, start); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 || tr.State() != running {
		t.Errorf("%d goroutines started the job, state %v", won, tr.State())
	}
}
