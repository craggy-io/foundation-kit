package trigger

import (
	"fmt"
	"time"
)

// Source describes an adapter that produces occurrences: a timer, file watch,
// external push, upstream change, or a structured event.
type Source struct {
	Kind     string        `json:"kind"`
	Interval time.Duration `json:"interval,omitempty"`
	Expr     string        `json:"expr,omitempty"`
	Root     string        `json:"root,omitempty"`
	Glob     string        `json:"glob,omitempty"`
	Upstream string        `json:"upstream,omitempty"`
	Event    *Spec         `json:"event,omitempty"`
}

// Validate checks generic source semantics; domain grants remain the host's.
func (s Source) Validate() error {
	switch s.Kind {
	case "every":
		if s.Interval <= 0 {
			return fmt.Errorf("every() needs a positive interval")
		}
	case "cron":
		_, err := ParseCron(s.Expr)
		return err
	case "watch":
		if s.Root == "" {
			return fmt.Errorf("watch() needs a root")
		}
	case "push":
	case "change":
		if s.Upstream == "" {
			return fmt.Errorf("on_change() needs an upstream")
		}
	case "event":
		if s.Event == nil {
			return fmt.Errorf("event source needs a specification")
		}
		return s.Event.Validate()
	default:
		return fmt.Errorf("unknown trigger source %q", s.Kind)
	}
	return nil
}

// Next returns the next timer occurrence, or zero for a non-timer source.
func (s Source) Next(from time.Time) time.Time {
	switch s.Kind {
	case "every":
		if s.Interval > 0 {
			return from.Add(s.Interval)
		}
	case "cron":
		if c, err := ParseCron(s.Expr); err == nil {
			return c.Next(from)
		}
	}
	return time.Time{}
}
