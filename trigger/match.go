package trigger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/craggy-io/foundation-kit/canonjson"
	"github.com/craggy-io/foundation-kit/event"
)

func document(e event.Envelope) (map[string]any, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	err = d.Decode(&doc)
	return doc, err
}

func lookup(doc map[string]any, path string) (any, bool) {
	var value any = doc
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func (p Predicate) matches(doc map[string]any) bool {
	switch p.Op {
	case "":
		return true
	case "all":
		for _, a := range p.Args {
			if !a.matches(doc) {
				return false
			}
		}
		return true
	case "any":
		for _, a := range p.Args {
			if a.matches(doc) {
				return true
			}
		}
		return false
	case "not":
		return !p.Args[0].matches(doc)
	}
	v, exists := lookup(doc, p.Path)
	if p.Op == "exists" {
		return exists
	}
	if !exists {
		return false
	}
	var want any
	d := json.NewDecoder(bytes.NewReader(p.Value))
	d.UseNumber()
	if d.Decode(&want) != nil {
		return false
	}
	if p.Op == "eq" {
		return equalJSON(v, want)
	}
	s, ok := v.(string)
	suffix, isString := want.(string)
	return ok && isString && strings.HasSuffix(strings.ToLower(s), strings.ToLower(suffix))
}

// Match returns mapped input only when the event matches. Missing mapped fields
// are errors, never silent nulls. Callers must surface these as failed admissions.
func (s Spec) Match(e event.Envelope) (json.RawMessage, bool, error) {
	if err := s.Validate(); err != nil {
		return nil, false, err
	}
	if e.Type != s.Event {
		return nil, false, nil
	}
	doc, err := document(e)
	if err != nil {
		return nil, false, err
	}
	if !s.Where.matches(doc) {
		return nil, false, nil
	}
	input := map[string]any{}
	for name, m := range s.Input {
		if m.Path == "" {
			input[name] = m.Value
			continue
		}
		v, ok := lookup(doc, m.Path)
		if !ok {
			return nil, true, fmt.Errorf("trigger: event lacks mapped field %s", m.Path)
		}
		input[name] = v
	}
	b, err := json.Marshal(input)
	return b, true, err
}

// Key identifies one admission within a binding and scope. A definition revision
// does not change business deduplication; an explicit retry creates a new attempt.
func (s Spec) Key(binding string, e event.Envelope) (string, error) {
	parts := []any{binding, e.Scope}
	if len(s.Deduplicate) == 0 {
		parts = append(parts, e.Source, e.ID)
	} else {
		doc, err := document(e)
		if err != nil {
			return "", err
		}
		for _, path := range s.Deduplicate {
			value, ok := lookup(doc, path)
			if !ok {
				return "", fmt.Errorf("trigger: event lacks deduplication field %s", path)
			}
			parts = append(parts, value)
		}
	}
	b, err := canonjson.Marshal(parts)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func equalJSON(a, b any) bool {
	left, err := canonjson.Marshal(a)
	if err != nil {
		return false
	}
	right, err := canonjson.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}
