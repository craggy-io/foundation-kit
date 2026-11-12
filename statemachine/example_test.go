package statemachine_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/craggy-io/foundation-kit/statemachine"
)

type State string
type Event string

const (
	Pending State = "pending"
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"

	Start  Event = "start"
	Finish Event = "finish"
	Fail   Event = "fail"
)

func Example() {
	jobs, err := statemachine.New(statemachine.Spec[State, Event]{
		Name:     "job",
		States:   []State{Pending, Running, Done, Failed},
		Initial:  Pending,
		Terminal: []State{Done, Failed},
		Transitions: []statemachine.Transition[State, Event]{
			{From: Pending, On: Start, To: Running},
			{From: Running, On: Fail, To: Failed},
			{From: Running, On: Finish, To: Done,
				Do: func(_ context.Context, step statemachine.Step[State, Event]) error {
					fmt.Println("saving result:", step.From, "->", step.To)
					return nil
				}},
		},
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	job := jobs.Start()

	_, err = job.Fire(ctx, Finish) // a pending job can't finish
	fmt.Println(errors.Is(err, statemachine.ErrNoTransition), job.State())

	job.Fire(ctx, Start)
	job.Fire(ctx, Finish)
	fmt.Println(job.State(), job.Done())
	// Output:
	// true pending
	// saving result: running -> done
	// done true
}

// A spec that doesn't hold together is refused, with every problem reported.
func ExampleNew_inconsistent() {
	_, err := statemachine.New(statemachine.Spec[State, Event]{
		Name:     "job",
		States:   []State{Pending, Running, Done, Failed},
		Initial:  Pending,
		Terminal: []State{Done},
		Transitions: []statemachine.Transition[State, Event]{
			{From: Pending, On: Start, To: Running},
			{From: Done, On: Fail, To: Failed},
		},
	})
	var invalid *statemachine.Error
	if errors.As(err, &invalid) {
		for _, d := range invalid.Diagnostics {
			fmt.Println(d.Code, d.State)
		}
	}
	// Output:
	// statemachine.terminal_exit done
	// statemachine.state_dead_end running
	// statemachine.state_unreachable failed
	// statemachine.state_dead_end failed
}
