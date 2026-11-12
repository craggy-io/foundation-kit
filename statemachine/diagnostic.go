package statemachine

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a stable, machine-readable diagnostic code. Match on the code,
// never on the message.
type Code string

const (
	CodeNameInvalid         Code = "statemachine.name_invalid"         // the machine has no name
	CodeStateDuplicate      Code = "statemachine.state_duplicate"      // a state is listed twice
	CodeStateUndeclared     Code = "statemachine.state_undeclared"     // a state is used but isn't in Spec.States
	CodeTransitionDuplicate Code = "statemachine.transition_duplicate" // a state has two transitions for one event
	CodeTerminalExit        Code = "statemachine.terminal_exit"        // a transition leaves a terminal state
	CodeStateUnreachable    Code = "statemachine.state_unreachable"    // no path leads from the initial state to a state
	CodeStateDeadEnd        Code = "statemachine.state_dead_end"       // a state has no way out and isn't terminal
	CodeStateTrapped        Code = "statemachine.state_trapped"        // a state has no path to any terminal state
)

// Diagnostic is one problem found while validating a Spec.
type Diagnostic struct {
	Code    Code   `json:"code"`
	State   string `json:"state,omitempty"` // the state at fault, formatted with %v
	Event   string `json:"event,omitempty"` // the event at fault, formatted with %v
	Message string `json:"message"`
}

// Error carries every diagnostic from one failed New.
type Error struct {
	Machine     string
	Diagnostics []Diagnostic
}

func (e *Error) Error() string {
	msgs := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		msgs[i] = d.Message
	}
	return fmt.Sprintf("statemachine %q: %s", e.Machine, strings.Join(msgs, "; "))
}

// Has reports whether any diagnostic carries code.
func (e *Error) Has(code Code) bool {
	for _, d := range e.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Errors from firing an event. Match them with errors.Is.
var (
	// ErrNoTransition means the current state has no transition for the event.
	// Every event fired at a terminal state returns it.
	ErrNoTransition = errors.New("no transition")
	// ErrUnknownState means a state isn't one of the machine's states, as
	// happens when a stored state outlives a change to the machine.
	ErrUnknownState = errors.New("unknown state")
	// ErrRefused means the transition's guard returned an error, which is
	// wrapped alongside it.
	ErrRefused = errors.New("guard refused")
	// ErrReentrant means a guard or action fired an event at the Tracker that
	// was running it.
	ErrReentrant = errors.New("event fired from inside a callback of the same tracker")
)
