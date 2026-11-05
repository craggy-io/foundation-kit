package bundle

import (
	"fmt"
	"strings"
)

// Severity says whether a Diagnostic rejects the load.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Code is a stable, machine-readable problem identifier. Tools and tests should
// match on Code rather than on Message, which may change.
type Code string

// Codes reported by Load. Compilers may add their own, namespaced by kind.
const (
	CodeSourceUnavailable    Code = "bundle.source_unavailable"
	CodeManifestMissing      Code = "bundle.manifest_missing"
	CodeManifestInvalid      Code = "bundle.manifest_invalid"
	CodeManifestNested       Code = "bundle.manifest_nested"
	CodePathForbidden        Code = "bundle.path_forbidden"
	CodeSymlinkForbidden     Code = "bundle.symlink_forbidden"
	CodeAssetMissing         Code = "bundle.asset_missing"
	CodeAssetUnreferenced    Code = "bundle.asset_unreferenced"
	CodeLimitExceeded        Code = "bundle.limit_exceeded"
	CodeDuplicateID          Code = "bundle.duplicate_id"
	CodeKindUnknown          Code = "bundle.kind_unknown"
	CodeKindInvalid          Code = "bundle.kind_invalid"
	CodeCompileFailed        Code = "bundle.compile_failed"
	CodeSchemaInvalid        Code = "bundle.schema_invalid"
	CodeReferenceMissing     Code = "bundle.reference_missing"
	CodeDependencyUndeclared Code = "bundle.dependency_undeclared"
	CodeDependencyMissing    Code = "bundle.dependency_missing"
	CodeDependencyMismatch   Code = "bundle.dependency_mismatch"
	CodeProviderInvalid      Code = "bundle.provider_invalid"
	CodeOverrideInvalid      Code = "bundle.override_invalid"
	CodeIDConflict           Code = "bundle.id_conflict"
)

// Diagnostic is one problem, in a form shared by CLIs, UIs, logs and agents. Fields
// that do not apply are left empty.
type Diagnostic struct {
	Code         Code     `json:"code"`
	Severity     Severity `json:"severity"`
	Bundle       string   `json:"bundle,omitempty"`
	Contribution string   `json:"contribution,omitempty"`
	Path         string   `json:"path,omitempty"`
	Line         int      `json:"line,omitempty"`
	Column       int      `json:"column,omitempty"`
	Message      string   `json:"message"`
	Related      []string `json:"related,omitempty"`
}

// LoadError is returned by Load when any Diagnostic has SeverityError.
type LoadError struct {
	// Diagnostics lists every problem found, in a deterministic order.
	Diagnostics []Diagnostic
}

// String formats d as "bundle:path:line:col: severity: message (code)",
// leaving out the parts d doesn't have.
func (d Diagnostic) String() string {
	var b strings.Builder
	for _, part := range []string{d.Bundle, d.Path} {
		if part != "" {
			b.WriteString(part + ":")
		}
	}
	if d.Line > 0 {
		fmt.Fprintf(&b, "%d:", d.Line)
		if d.Column > 0 {
			fmt.Fprintf(&b, "%d:", d.Column)
		}
	}
	if b.Len() > 0 {
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "%s: %s (%s)", d.Severity, d.Message, d.Code)
	return b.String()
}

// Error lists every diagnostic, one per line, each formatted by
// Diagnostic.String.
func (e *LoadError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "bundle: load failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "bundle: load failed with %d problem(s):", len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		b.WriteString("\n  " + d.String())
	}
	return b.String()
}
