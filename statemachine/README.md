# statemachine

A small finite state machine. You declare your own states, events and transitions in a `Spec`. `New` checks that they are consistent and returns an immutable `Machine`, or an `*Error` listing every problem it found.

```go
jobs, err := statemachine.New(statemachine.Spec[State, Event]{
    Name:     "job",
    States:   []State{Pending, Running, Done, Failed},
    Initial:  Pending,
    Terminal: []State{Done, Failed},
    Transitions: []statemachine.Transition[State, Event]{
        {From: Pending, On: Start, To: Running, Guard: hasWorkspace},
        {From: Running, On: Finish, To: Done, Do: saveResult},
        {From: Running, On: Fail, To: Failed},
    },
})
```

`State` and `Event` are your own comparable types, usually a string or integer type with constants.

## Running a machine

A `Machine` holds no current state, so you choose where state lives.

```go
// State stored elsewhere, such as a database row: you pass it in and store the result.
next, err := jobs.Fire(ctx, row.State, Finish)

// State held in memory: a Tracker keeps it and runs one Fire at a time.
job := jobs.Start()            // or jobs.Resume(storedState)
state, err := job.Fire(ctx, Start)
```

`Fire` runs the transition's `Guard`, then its `Do`. If either returns an error the state doesn't change, although whatever the callback already did is not undone. Match errors with `errors.Is`:

| Error | Meaning |
| --- | --- |
| `ErrNoTransition` | The current state has no transition for the event. Every event at a terminal state returns it. |
| `ErrUnknownState` | The state isn't one of the machine's, as when a stored state outlives a change to the machine. |
| `ErrRefused` | The guard returned an error, which is wrapped alongside. |
| `ErrReentrant` | A callback fired an event at the `Tracker` that was running it. |

`ErrReentrant` relies on the callback passing on the context it was given. A callback that fires at its own `Tracker` with a different context deadlocks.

`Next` looks up where an event leads without running any callback, and `Events` lists the events a state accepts.

## What New checks

Each problem is a `Diagnostic` with a stable `Code`. Match on the code, never the message.

| Code | Problem |
| --- | --- |
| `statemachine.name_invalid` | The machine has no name |
| `statemachine.state_duplicate` | A state is listed twice |
| `statemachine.state_undeclared` | The initial state, a terminal state or a transition uses a state that isn't in `States` |
| `statemachine.transition_duplicate` | A state has two transitions for one event |
| `statemachine.terminal_exit` | A transition leaves a terminal state |
| `statemachine.state_unreachable` | No path leads from the initial state to a state |
| `statemachine.state_dead_end` | A state has no way out and isn't terminal |
| `statemachine.state_trapped` | A state has no path to any terminal state. Checked only when terminal states are declared. |

Consumers: none yet.

## Implementation status

- [x] `Spec` validation that reports every problem, with stable codes
- [x] `Machine.Next`, `Machine.Fire`, `Machine.Events`
- [x] Guards and actions on transitions, with the state unchanged when one fails
- [x] `Tracker` with serialised `Fire`, `Resume` from a stored state, and reentrancy detection
- [ ] Callbacks on entering or leaving a state (use `Do` on each transition for now)
- [ ] Several guarded transitions for one state and event (use separate events for now)
- [ ] Nested or parallel states, timers and persistence (not planned: state storage stays with the consumer)
