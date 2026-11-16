// Package event defines immutable, versioned facts published by a host.
// Publishers supply IDs; hosts establish source, scope and causation. A fact
// describes one occurrence, unlike a coalescible refresh notification.
package event

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var typeName = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+@[1-9][0-9]*$`)

// Envelope is one occurrence. Data must be a JSON object. Scope is opaque to
// the library; only the receiving host can establish its authority.
type Envelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Scope     string          `json:"scope"`
	Subject   string          `json:"subject,omitempty"`
	Time      time.Time       `json:"time"`
	Causation []string        `json:"causation,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// ValidType reports whether a type includes its namespace and version.
func ValidType(name string) bool { return typeName.MatchString(name) }

// Validate rejects incomplete envelopes and non-object payloads.
func (e Envelope) Validate() error {
	if e.ID == "" || e.Source == "" || !ValidType(e.Type) || e.Time.IsZero() {
		return fmt.Errorf("event: id, source, versioned type and time are required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &object); err != nil || object == nil {
		return fmt.Errorf("event: data must be a JSON object")
	}
	return nil
}
