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
	// TZ is the IANA zone a cron expression is read in; UTC when empty.
	TZ       string `json:"tz,omitempty"`
	Root     string `json:"root,omitempty"`
	Glob     string `json:"glob,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Event    *Spec  `json:"event,omitempty"`
}

// Validate checks generic source semantics; domain grants remain the host's.
func (s Source) Validate() error {
	switch s.Kind {
	case "every":
		if s.Interval <= 0 {
			return fmt.Errorf("every() needs a positive interval")
		}
	case "cron":
		if _, err := s.location(); err != nil {
			return err
		}
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
		loc, err := s.location()
		if c, perr := ParseCron(s.Expr); err == nil && perr == nil {
			return c.Next(from.In(loc))
		}
	}
	return time.Time{}
}

func (s Source) location() (*time.Location, error) {
	if s.TZ == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		return nil, fmt.Errorf("cron() zone %q: %w", s.TZ, err)
	}
	return loc, nil
}
