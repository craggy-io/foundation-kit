// Package trigger matches structured events and maps them to durable work.
// Specifications are immutable after validation. Hosts own scope, authority,
// event storage and execution; this package owns matching and admission rules.
package trigger

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/craggy-io/foundation-kit/event"
)

// Predicate is a bounded expression tree. An empty predicate matches all.
type Predicate struct {
	Op    string          `json:"op,omitempty"`
	Path  string          `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	Args  []Predicate     `json:"args,omitempty"`
}

// Mapping selects an event field or supplies a literal JSON value.
type Mapping struct {
	Path  string          `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// Spec describes when to admit work. Replay explicitly includes stored history.
// Manual holds admissions for release. Deduplicate selects a business key;
// without it each source/event ID is a distinct admission.
type Spec struct {
	Clock       *Source            `json:"clock,omitempty"`
	Event       string             `json:"event"`
	Where       Predicate          `json:"where,omitempty"`
	Input       map[string]Mapping `json:"input,omitempty"`
	Deduplicate []string           `json:"deduplicate,omitempty"`
	Manual      bool               `json:"manual,omitempty"`
	Replay      bool               `json:"replay,omitempty"`
}

// Validate checks the full expression before a host activates it.
func (s Spec) Validate() error {
	if s.Clock != nil {
		if s.Event != "clock.tick@1" || (s.Clock.Kind != "every" && s.Clock.Kind != "cron") {
			return fmt.Errorf("trigger: clock must be every or cron on clock.tick@1")
		}
		if err := s.Clock.Validate(); err != nil {
			return err
		}
	}
	if !event.ValidType(s.Event) {
		return fmt.Errorf("trigger: invalid event type %q", s.Event)
	}
	if err := s.Where.validate(0); err != nil {
		return err
	}
	for name, m := range s.Input {
		if name == "" {
			return fmt.Errorf("trigger: empty input name")
		}
		if err := m.validate(); err != nil {
			return fmt.Errorf("trigger input %s: %w", name, err)
		}
	}
	for _, p := range s.Deduplicate {
		if !validPath(p) {
			return fmt.Errorf("trigger: invalid deduplication path %q", p)
		}
	}
	return nil
}

func validPath(p string) bool {
	parts := strings.Split(p, ".")
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	switch parts[0] {
	case "id", "type", "source", "scope", "subject", "time":
		return len(parts) == 1
	case "data":
		return true
	default:
		return false
	}
}

func (m Mapping) validate() error {
	if m.Path != "" {
		if !validPath(m.Path) || len(m.Value) != 0 {
			return fmt.Errorf("field mapping must contain only a valid path")
		}
	} else if !json.Valid(m.Value) {
		return fmt.Errorf("literal mapping requires valid JSON")
	}
	return nil
}

func (p Predicate) validate(depth int) error {
	if depth > 16 {
		return fmt.Errorf("trigger: predicates exceed nesting limit")
	}
	switch p.Op {
	case "":
		if p.Path != "" || len(p.Value)+len(p.Args) != 0 {
			return fmt.Errorf("trigger: empty operator has operands")
		}
	case "all", "any", "not":
		if len(p.Args) == 0 || (p.Op == "not" && len(p.Args) != 1) || p.Path != "" || len(p.Value) != 0 {
			return fmt.Errorf("trigger: invalid %s operands", p.Op)
		}
		for _, arg := range p.Args {
			if err := arg.validate(depth + 1); err != nil {
				return err
			}
		}
	case "eq", "ends_with", "exists":
		return p.validateField()
	default:
		return fmt.Errorf("trigger: unknown predicate %q", p.Op)
	}
	return nil
}

func (p Predicate) validateField() error {
	if !validPath(p.Path) || len(p.Args) != 0 {
		return fmt.Errorf("trigger: invalid predicate path %q", p.Path)
	}
	if p.Op != "exists" && !json.Valid(p.Value) {
		return fmt.Errorf("trigger: predicate needs a JSON value")
	}
	if p.Op == "ends_with" {
		var s string
		if json.Unmarshal(p.Value, &s) != nil {
			return fmt.Errorf("trigger: ends_with needs a string")
		}
	}
	return nil
}
