package archtest

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Codes identify a kind of problem. They are stable, so match on them rather than on
// messages.
const (
	codeConfigInvalid    = "repo.config_invalid"    // archtest.yaml or a Config can't be used
	codeConfigConflict   = "repo.config_conflict"   // a requirement points into an excluded path
	codeIncomplete       = "repo.incomplete"        // part of the repository couldn't be checked
	codeLimitExceeded    = "repo.limit_exceeded"    // a size or count limit stopped the check
	codeSelectorEmpty    = "repo.selector_empty"    // a select include matched nothing
	codeExceptionStale   = "repo.exception_stale"   // an exception suppressed nothing
	codeEntryMissing     = "repo.entry_missing"     // a required file or directory is absent
	codeEntryForbidden   = "repo.entry_forbidden"   // a forbidden file or directory is present
	codeEntryUnexpected  = "repo.entry_unexpected"  // an entry isn't in a closed allow list
	codeEntryUnsupported = "repo.entry_unsupported" // a symlink, junction or other special entry
	codeCaseCollision    = "repo.case_collision"    // two names differ only in case
	codeChildCount       = "repo.child_count"       // too few child directories
	codeParseFailed      = "repo.parse_failed"      // a file isn't valid in its format
	codeSchemaInvalid    = "repo.schema_invalid"    // a value doesn't match its schema
	codeImportForbidden  = "repo.import_forbidden"  // a Go file or package imports a forbidden path
	codeDirective        = "repo.directive_forbidden"
	codeFrontmatter      = "repo.frontmatter_invalid"
	codeFrontmatterMiss  = "repo.frontmatter_missing"
	codeHeadingMissing   = "repo.heading_missing"
	codeHeadingCount     = "repo.heading_count"     // a heading appears the wrong number of times
	codeHeadingOrder     = "repo.heading_order"     // headings are out of order
	codeSectionEmpty     = "repo.section_empty"     // a section has no content
	codePatternUnmatched = "repo.pattern_unmatched" // a local import pattern matches no package
	codePackageUncovered = "repo.package_uncovered" // a package isn't in the inventory
	codeReplace          = "repo.replace_forbidden" // go.mod has a replace directive
)

// diagnostic is one problem. Path is relative to the root being checked and uses
// "/". Line and Column are 1-based, and zero when there is no real position.
type diagnostic struct {
	Code    string
	Rule    string
	Path    string
	Line    int
	Column  int
	Pointer string // JSON Pointer into a structured value, if any
	Message string
	Why     string

	// RuleLine and RuleColumn locate the rule in the configuration file, if it has one.
	RuleLine, RuleColumn int
}

func compareDiagnostics(a, b diagnostic) int {
	return cmp.Or(
		cmp.Compare(a.Path, b.Path),
		cmp.Compare(a.Line, b.Line),
		cmp.Compare(a.Column, b.Column),
		cmp.Compare(a.Rule, b.Rule),
		cmp.Compare(a.Code, b.Code),
		cmp.Compare(a.Pointer, b.Pointer),
		cmp.Compare(a.Message, b.Message),
		cmp.Compare(a.Why, b.Why),
		cmp.Compare(a.RuleLine, b.RuleLine),
		cmp.Compare(a.RuleColumn, b.RuleColumn),
	)
}

// sortDiagnostics orders diagnostics deterministically and drops exact duplicates.
func sortDiagnostics(ds []diagnostic) []diagnostic {
	slices.SortFunc(ds, compareDiagnostics)
	return slices.Compact(ds)
}

// format renders one diagnostic for a test failure. config names the configuration
// file for the "rule defined at" line, or is empty for a Go Config.
func (d diagnostic) format(config string) string {
	var b strings.Builder
	b.WriteString(d.Path)
	if d.Line > 0 {
		fmt.Fprintf(&b, ":%d", d.Line)
		if d.Column > 0 {
			fmt.Fprintf(&b, ":%d", d.Column)
		}
	}
	if d.Rule != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "[%s]", d.Rule)
	}
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(d.Code)
	if d.Pointer != "" {
		b.WriteString(" " + d.Pointer)
	}
	b.WriteString(": " + d.Message)
	if d.Why != "" {
		b.WriteString("\n    why: " + d.Why)
	}
	if config != "" && d.RuleLine > 0 {
		fmt.Fprintf(&b, "\n    rule defined at %s:%d:%d", config, d.RuleLine, d.RuleColumn)
	}
	return b.String()
}

// report formats sorted diagnostics as one failure message.
func report(header string, ds []diagnostic, config string) string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.format(config)
	}
	return fmt.Sprintf("%s: %d problem(s):\n  %s", header, len(ds), strings.Join(lines, "\n  "))
}
