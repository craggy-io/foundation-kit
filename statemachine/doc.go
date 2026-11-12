// Package statemachine is a small finite state machine: a consumer declares its
// own states, events and transitions, the package checks that they are
// consistent, and then moves a state through them.
//
// A Spec lists every state, the initial and terminal states, and the
// transitions, each with an optional guard and action. New validates the whole
// Spec and returns an immutable Machine, or an *Error carrying every problem it
// found. A Machine holds no current state, so the consumer chooses where state
// lives:
//
//   - Machine.Fire takes the current state and an event, runs the transition's
//     guard and action, and returns the next state. Use it when the state is
//     stored elsewhere, such as in a database row.
//   - A Tracker, from Machine.Start or Machine.Resume, holds the current state
//     in memory and serialises events fired at it.
//
// The package owns the mechanism only. The states, events and their meaning
// stay with the consumer, and there is no shared set of states.
//
// # Invariants
//
//   - A Machine that New returned has passed every check: each state is listed
//     once and can be reached from the initial state, each state and event pair
//     has at most one transition, nothing leaves a terminal state, and every
//     other state has a way out and, when terminal states are declared, a path
//     to one.
//   - Firing an event the current state has no transition for changes nothing
//     and returns ErrNoTransition.
//   - When a guard or action returns an error, the state doesn't change. What
//     the callback already did is not undone.
//   - A guard runs before the action, and the action runs before the new state
//     is returned or stored.
//
// # Concurrency
//
// A Machine is immutable and safe for concurrent use, so guards and actions must
// be too. A Tracker is safe for concurrent use and runs one Fire at a time. A
// callback that fires an event at its own Tracker gets ErrReentrant, provided it
// passes on the context it was given. Without that context the call deadlocks.
//
// Exported names: Machine, Spec, Transition, Step, Tracker, New, Code,
// Diagnostic, Error, ErrNoTransition, ErrUnknownState, ErrRefused, ErrReentrant.
package statemachine
