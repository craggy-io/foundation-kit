package taxonomy

import "strings"

// Code is a stable, machine-readable diagnostic code. Match on the code,
// never on the message.
type Code string

const (
	CodeOwnerInvalid    Code = "taxonomy.owner_invalid"    // an owner is empty or listed twice
	CodeNameInvalid     Code = "taxonomy.name_invalid"     // a type name is malformed
	CodeTypeConflict    Code = "taxonomy.type_conflict"    // two owners declare one type differently
	CodeParentUnknown   Code = "taxonomy.parent_unknown"   // a parent isn't declared by anyone
	CodeCycle           Code = "taxonomy.cycle"            // a type is its own ancestor
	CodeNamespaceEscape Code = "taxonomy.namespace_escape" // a type doesn't descend from its declared namespace
	CodeOwnerRequired   Code = "taxonomy.owner_required"   // other owners' types still depend on a withdrawn owner
)

// Diagnostic is one problem found while validating types.
type Diagnostic struct {
	Code    Code   `json:"code"`
	Type    string `json:"type,omitempty"`  // the type at fault
	Owner   string `json:"owner,omitempty"` // who declared it
	Message string `json:"message"`
}

// Error carries every diagnostic from one failed validation.
type Error struct {
	Diagnostics []Diagnostic
}

func (e *Error) Error() string {
	msgs := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		msgs[i] = d.Message
	}
	return "taxonomy: " + strings.Join(msgs, "; ")
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
