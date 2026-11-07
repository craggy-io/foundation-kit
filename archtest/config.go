package archtest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/craggy-io/foundation-kit/internal/strictyaml"
	"github.com/craggy-io/foundation-kit/kind"
	"github.com/craggy-io/foundation-kit/kind/jsonschema"
)

// configVersion is the only archtest.yaml version supported so far.
const configVersion = 1

// fileConfig is a decoded archtest.yaml. Every field in the file maps to a field
// here; decode rejects any other.
type fileConfig struct {
	Exclude []exclusion
	Rules   []*fileRule
}

// pos is a position in archtest.yaml.
type pos struct{ line, column int }

func at(n *strictyaml.Node) pos { return pos{n.Line, n.Column} }

type exclusion struct {
	Path, Why string
	pos
}

type exception struct {
	Path, Why string
	pos
	used bool
}

type fileRule struct {
	ID, Why    string
	Exceptions []*exception
	pos

	Select     *selector
	Tree       *treeCheck
	GoSource   *goSourceCheck
	Markdown   *markdownCheck
	Structured *structuredCheck
	Imports    *importsCheck
}

// Values of selector.Kind.
const (
	kindFile      = "file"
	kindDirectory = "directory"
)

type selector struct {
	Kind             string
	Include, Exclude []string
	AllowEmpty       bool // on_empty: allow
	EmptyReason      string
	pos
}

type treeCheck struct {
	RequireFiles, RequireDirs []string
	Allow                     bool // whether an allow mapping is present, closing the list
	AllowFiles, AllowDirs     []string
	Forbid                    []string
	Children                  *childCheck
}

type childCheck struct {
	Min               int
	RequireFiles      []string
	ForbidDescendants []string
}

type goSourceCheck struct {
	ForbidImports, ForbidDirectives []string
}

type markdownCheck struct {
	Frontmatter *frontmatterCheck
	Headings    []headingCheck
	Order       []string
}

type frontmatterCheck struct {
	Required bool
	Schema   *schemaRef
}

// Values of headingCheck.Body.
const (
	bodyAny      = "any"
	bodyNonempty = "nonempty"
)

type headingCheck struct {
	Text  string
	Level int
	Count int
	Body  string
}

// Values of structuredCheck.Format.
const (
	formatAuto = "auto"
	formatJSON = "json"
	formatYAML = "yaml"
)

type structuredCheck struct {
	Format string
	Schema *schemaRef
}

// schemaRef is an inline JSON Schema or a schema file. Inline schemas compile while
// decoding; files compile once the repository root is known.
type schemaRef struct {
	File string
	pos
	compiled kind.Schema
}

type importsCheck struct {
	Module                      string
	Packages                    []string
	HasPackages                 bool
	From, Except, Forbid, Allow []string
	AllowReplace                bool
	modulePos                   pos
	packagesPos                 pos
}

// decoder turns a strictyaml tree into a fileConfig, collecting every problem.
type decoder struct {
	file  string // configuration file name, for diagnostics
	rule  string // ID of the rule being decoded, for diagnostics
	diags []diagnostic
}

func (d *decoder) errorf(p pos, format string, args ...any) {
	d.diags = append(d.diags, diagnostic{Code: codeConfigInvalid, Rule: d.rule, Path: d.file,
		Line: p.line, Column: p.column, Message: fmt.Sprintf(format, args...)})
}

// decodeConfig parses and validates archtest.yaml without touching the repository.
func decodeConfig(file string, data []byte) (*fileConfig, []diagnostic) {
	d := &decoder{file: file}
	root, yamlDiags := strictyaml.Parse(data)
	for _, y := range yamlDiags {
		d.errorf(pos{y.Line, y.Column}, "%s", y.Message)
	}
	if len(yamlDiags) > 0 {
		return nil, d.diags
	}
	if root == nil {
		d.errorf(pos{1, 1}, "the file is empty")
		return nil, d.diags
	}

	c := &fileConfig{}
	sawVersion, sawRules := false, false
	d.fields(root, "the configuration", map[string]func(*strictyaml.Node){
		"version": func(n *strictyaml.Node) {
			sawVersion = true
			if v, ok := d.integer(n, "version"); ok && v != configVersion {
				d.errorf(at(n), "version %d isn't supported; use %d", v, configVersion)
			}
		},
		"exclude": func(n *strictyaml.Node) {
			d.each(n, "exclude", func(item *strictyaml.Node, where string) {
				var e exclusion
				e.pos = at(item)
				d.fields(item, where, map[string]func(*strictyaml.Node){
					"path": func(v *strictyaml.Node) { e.Path = d.path(v, where+".path", true, false) },
					"why":  func(v *strictyaml.Node) { e.Why = d.str(v, where+".why") },
				})
				d.require(item, where, "path", "why")
				c.Exclude = append(c.Exclude, e)
			})
		},
		"rules": func(n *strictyaml.Node) {
			sawRules = true
			d.each(n, "rules", func(item *strictyaml.Node, where string) {
				if r := d.decodeRule(item, where); r != nil {
					c.Rules = append(c.Rules, r)
				}
			})
			if n.Kind == strictyaml.Sequence && len(n.Items) == 0 {
				d.errorf(at(n), "rules is empty, so nothing is checked")
			}
		},
	})
	if root.Kind == strictyaml.Mapping {
		if !sawVersion {
			d.errorf(at(root), "version is required; use %d", configVersion)
		}
		if !sawRules {
			d.errorf(at(root), "rules is required")
		}
	}

	ids := map[string]pos{}
	for _, r := range c.Rules {
		if first, dup := ids[r.ID]; dup && r.ID != "" {
			d.rule = r.ID
			d.errorf(r.pos, "rule id %q is already used on line %d", r.ID, first.line)
		}
		ids[r.ID] = r.pos
	}
	d.rule = ""
	d.checkImportInventories(c)
	if len(d.diags) > 0 {
		return nil, d.diags
	}
	return c, nil
}

// families are the rule payloads; a rule has exactly one.
var families = []string{"tree", "go_source", "markdown", "structured", "imports"}

func (d *decoder) decodeRule(n *strictyaml.Node, where string) *fileRule {
	r := &fileRule{pos: at(n)}
	if n.Kind == strictyaml.Mapping {
		for _, p := range n.Pairs {
			if p.Key == "id" && p.Value.Kind == strictyaml.String {
				r.ID = p.Value.Value
				where = fmt.Sprintf("rule %q", r.ID)
			}
		}
	}
	d.rule = r.ID
	defer func() { d.rule = "" }()

	var payloads []string
	var selectNode *strictyaml.Node
	d.fields(n, where, map[string]func(*strictyaml.Node){
		"id": func(v *strictyaml.Node) {
			if d.str(v, "id") == "" && v.Kind == strictyaml.String {
				d.errorf(at(v), "id is empty")
			}
		},
		"why": func(v *strictyaml.Node) {
			if r.Why = d.str(v, "why"); r.Why == "" && v.Kind == strictyaml.String {
				d.errorf(at(v), "why is empty; say what to do instead")
			}
		},
		"exceptions": func(v *strictyaml.Node) {
			d.each(v, "exceptions", func(item *strictyaml.Node, at2 string) {
				e := &exception{pos: at(item)}
				d.fields(item, at2, map[string]func(*strictyaml.Node){
					"path": func(v *strictyaml.Node) { e.Path = d.path(v, at2+".path", false, false) },
					"why":  func(v *strictyaml.Node) { e.Why = d.str(v, at2+".why") },
				})
				d.require(item, at2, "path", "why")
				r.Exceptions = append(r.Exceptions, e)
			})
		},
		"select": func(v *strictyaml.Node) { selectNode = v; r.Select = d.decodeSelector(v) },
		"tree": func(v *strictyaml.Node) {
			payloads = append(payloads, "tree")
			r.Tree = d.decodeTree(v)
		},
		"go_source": func(v *strictyaml.Node) {
			payloads = append(payloads, "go_source")
			r.GoSource = d.decodeGoSource(v)
		},
		"markdown": func(v *strictyaml.Node) {
			payloads = append(payloads, "markdown")
			r.Markdown = d.decodeMarkdown(v)
		},
		"structured": func(v *strictyaml.Node) {
			payloads = append(payloads, "structured")
			r.Structured = d.decodeStructured(v)
		},
		"imports": func(v *strictyaml.Node) {
			payloads = append(payloads, "imports")
			r.Imports = d.decodeImports(v)
		},
	})
	if n.Kind != strictyaml.Mapping {
		return nil
	}
	d.require(n, where, "id", "why")
	switch len(payloads) {
	case 0:
		d.errorf(r.pos, "%s has no check; give it one of %s", where, strings.Join(families, ", "))
		return r
	case 1:
	default:
		d.errorf(r.pos, "%s has %s; give each rule exactly one check", where, strings.Join(payloads, " and "))
		return r
	}

	wantKind := map[string]string{"tree": kindDirectory, "go_source": kindFile, "markdown": kindFile, "structured": kindFile}[payloads[0]]
	switch {
	case payloads[0] == "imports":
		if selectNode != nil {
			d.errorf(at(selectNode), "an imports rule has no select; it names packages with from")
		}
		if len(r.Exceptions) > 0 {
			d.errorf(r.Exceptions[0].pos, "an imports rule has no exceptions; use except or allow")
		}
	case r.Select == nil:
		if selectNode == nil {
			d.errorf(r.pos, "%s needs a select", where)
		}
	case r.Select.Kind != "" && r.Select.Kind != wantKind:
		d.errorf(r.Select.pos, "a %s rule selects kind %s, not %s", payloads[0], wantKind, r.Select.Kind)
	}
	return r
}

func (d *decoder) decodeSelector(n *strictyaml.Node) *selector {
	s := &selector{pos: at(n)}
	onEmpty := ""
	d.fields(n, "select", map[string]func(*strictyaml.Node){
		"kind": func(v *strictyaml.Node) {
			s.Kind = d.oneOf(v, "select.kind", kindFile, kindDirectory)
		},
		"include": func(v *strictyaml.Node) {
			s.Include = d.paths(v, "select.include", true, false)
			if len(s.Include) == 0 && v.Kind == strictyaml.Sequence {
				d.errorf(at(v), "select.include is empty")
			}
		},
		"exclude":      func(v *strictyaml.Node) { s.Exclude = d.paths(v, "select.exclude", true, false) },
		"on_empty":     func(v *strictyaml.Node) { onEmpty = d.oneOf(v, "select.on_empty", "error", "allow") },
		"empty_reason": func(v *strictyaml.Node) { s.EmptyReason = d.str(v, "select.empty_reason") },
	})
	d.require(n, "select", "kind", "include")
	s.AllowEmpty = onEmpty == "allow"
	switch {
	case s.AllowEmpty && s.EmptyReason == "":
		d.errorf(s.pos, "on_empty: allow needs an empty_reason saying why the scope is optional")
	case !s.AllowEmpty && s.EmptyReason != "":
		d.errorf(s.pos, "empty_reason is only used with on_empty: allow")
	}
	return s
}

func (d *decoder) decodeTree(n *strictyaml.Node) *treeCheck {
	t := &treeCheck{}
	d.fields(n, "tree", map[string]func(*strictyaml.Node){
		"require": func(v *strictyaml.Node) {
			d.fields(v, "tree.require", map[string]func(*strictyaml.Node){
				"files":       func(v *strictyaml.Node) { t.RequireFiles = d.paths(v, "tree.require.files", true, true) },
				"directories": func(v *strictyaml.Node) { t.RequireDirs = d.paths(v, "tree.require.directories", true, true) },
			})
		},
		"allow": func(v *strictyaml.Node) {
			t.Allow = true
			d.fields(v, "tree.allow", map[string]func(*strictyaml.Node){
				"files":       func(v *strictyaml.Node) { t.AllowFiles = d.paths(v, "tree.allow.files", true, true) },
				"directories": func(v *strictyaml.Node) { t.AllowDirs = d.paths(v, "tree.allow.directories", true, true) },
			})
		},
		"forbid": func(v *strictyaml.Node) { t.Forbid = d.paths(v, "tree.forbid", true, true) },
		"child_directories": func(v *strictyaml.Node) {
			c := &childCheck{}
			d.fields(v, "tree.child_directories", map[string]func(*strictyaml.Node){
				"min": func(v *strictyaml.Node) {
					if m, ok := d.integer(v, "min"); ok {
						if m < 0 {
							d.errorf(at(v), "min can't be negative")
						}
						c.Min = m
					}
				},
				"require_files":      func(v *strictyaml.Node) { c.RequireFiles = d.paths(v, "require_files", true, true) },
				"forbid_descendants": func(v *strictyaml.Node) { c.ForbidDescendants = d.paths(v, "forbid_descendants", true, false) },
			})
			if c.Min == 0 && len(c.RequireFiles) == 0 && len(c.ForbidDescendants) == 0 {
				d.errorf(at(v), "tree.child_directories checks nothing")
			}
			t.Children = c
		},
	})
	if n.Kind != strictyaml.Mapping {
		return t
	}
	if len(t.RequireFiles)+len(t.RequireDirs)+len(t.Forbid) == 0 && !t.Allow && t.Children == nil {
		d.errorf(at(n), "tree checks nothing")
	}
	for _, req := range [][]string{t.RequireFiles, t.RequireDirs} {
		for _, r := range req {
			if slices.Contains(t.Forbid, r) {
				d.errorf(at(n), "tree both requires and forbids %q", r)
			}
		}
	}
	if t.Allow {
		for _, r := range t.RequireFiles {
			if isLiteral(r) && !matchAnyName(t.AllowFiles, r) {
				d.errorf(at(n), "tree requires file %q but its allow list doesn't permit it", r)
			}
		}
		for _, r := range t.RequireDirs {
			if isLiteral(r) && !matchAnyName(t.AllowDirs, r) {
				d.errorf(at(n), "tree requires directory %q but its allow list doesn't permit it", r)
			}
		}
	}
	return t
}

func (d *decoder) decodeGoSource(n *strictyaml.Node) *goSourceCheck {
	g := &goSourceCheck{}
	d.fields(n, "go_source", map[string]func(*strictyaml.Node){
		"forbid_imports": func(v *strictyaml.Node) {
			g.ForbidImports = d.strs(v, "go_source.forbid_imports")
			for _, p := range g.ForbidImports {
				if p == "" || strings.ContainsAny(p, " \t\"\\") || strings.HasPrefix(p, "./") {
					d.errorf(at(v), "go_source.forbid_imports: %q isn't an import path", p)
				}
			}
		},
		"forbid_directives": func(v *strictyaml.Node) {
			g.ForbidDirectives = d.strs(v, "go_source.forbid_directives")
			for _, p := range g.ForbidDirectives {
				if p == "" || strings.HasPrefix(p, "//") || strings.ContainsAny(p, " \t") {
					d.errorf(at(v), "go_source.forbid_directives: %q must be a directive name such as go:embed", p)
				}
			}
		},
	})
	if n.Kind == strictyaml.Mapping && len(g.ForbidImports)+len(g.ForbidDirectives) == 0 {
		d.errorf(at(n), "go_source checks nothing")
	}
	return g
}

func (d *decoder) decodeMarkdown(n *strictyaml.Node) *markdownCheck {
	m := &markdownCheck{}
	var orderNode *strictyaml.Node
	d.fields(n, "markdown", map[string]func(*strictyaml.Node){
		"frontmatter": func(v *strictyaml.Node) {
			f := &frontmatterCheck{Required: true}
			d.fields(v, "markdown.frontmatter", map[string]func(*strictyaml.Node){
				"required":    func(v *strictyaml.Node) { f.Required = d.boolean(v, "required") },
				"schema":      func(v *strictyaml.Node) { f.Schema = d.inlineSchema(v, f.Schema) },
				"schema_file": func(v *strictyaml.Node) { f.Schema = d.schemaFile(v, f.Schema) },
			})
			m.Frontmatter = f
		},
		"headings": func(v *strictyaml.Node) {
			d.each(v, "markdown.headings", func(item *strictyaml.Node, where string) {
				h := headingCheck{Count: 1, Body: bodyAny}
				d.fields(item, where, map[string]func(*strictyaml.Node){
					"text": func(v *strictyaml.Node) {
						if h.Text = d.str(v, "text"); strings.Join(strings.Fields(h.Text), " ") != h.Text || h.Text == "" {
							d.errorf(at(v), "heading text %q must be nonempty, trimmed and use single spaces", h.Text)
						}
					},
					"level": func(v *strictyaml.Node) {
						if l, ok := d.integer(v, "level"); ok {
							if l < 1 || l > 6 {
								d.errorf(at(v), "level must be 1 to 6")
							}
							h.Level = l
						}
					},
					"count": func(v *strictyaml.Node) {
						if c, ok := d.integer(v, "count"); ok {
							if c < 0 {
								d.errorf(at(v), "count can't be negative")
							}
							h.Count = c
						}
					},
					"body": func(v *strictyaml.Node) { h.Body = d.oneOf(v, "body", bodyAny, bodyNonempty) },
				})
				d.require(item, where, "text", "level")
				m.Headings = append(m.Headings, h)
			})
		},
		"order": func(v *strictyaml.Node) { orderNode = v; m.Order = d.strs(v, "markdown.order") },
	})
	if n.Kind != strictyaml.Mapping {
		return m
	}
	if m.Frontmatter == nil && len(m.Headings) == 0 {
		d.errorf(at(n), "markdown checks nothing")
	}
	if f := m.Frontmatter; f != nil && !f.Required && f.Schema == nil {
		d.errorf(at(n), "markdown.frontmatter with required: false and no schema checks nothing")
	}
	for _, text := range m.Order {
		var matches []headingCheck
		for _, h := range m.Headings {
			if h.Text == text {
				matches = append(matches, h)
			}
		}
		if len(matches) != 1 || matches[0].Count != 1 {
			d.errorf(at(orderNode), "markdown.order: %q must name exactly one heading with count 1", text)
		}
	}
	return m
}

func (d *decoder) decodeStructured(n *strictyaml.Node) *structuredCheck {
	s := &structuredCheck{Format: formatAuto}
	d.fields(n, "structured", map[string]func(*strictyaml.Node){
		"format": func(v *strictyaml.Node) {
			s.Format = d.oneOf(v, "structured.format", formatAuto, formatJSON, formatYAML)
		},
		"schema":      func(v *strictyaml.Node) { s.Schema = d.inlineSchema(v, s.Schema) },
		"schema_file": func(v *strictyaml.Node) { s.Schema = d.schemaFile(v, s.Schema) },
	})
	if n.Kind == strictyaml.Mapping && s.Schema == nil {
		d.errorf(at(n), "structured needs a schema or schema_file")
	}
	return s
}

func (d *decoder) decodeImports(n *strictyaml.Node) *importsCheck {
	c := &importsCheck{}
	d.fields(n, "imports", map[string]func(*strictyaml.Node){
		"module": func(v *strictyaml.Node) {
			c.modulePos = at(v)
			c.Module = d.path(v, "imports.module", false, false)
		},
		"packages": func(v *strictyaml.Node) {
			c.HasPackages, c.packagesPos = true, at(v)
			c.Packages = d.strs(v, "imports.packages")
		},
		"from":          func(v *strictyaml.Node) { c.From = d.strs(v, "imports.from") },
		"except":        func(v *strictyaml.Node) { c.Except = d.strs(v, "imports.except") },
		"forbid":        func(v *strictyaml.Node) { c.Forbid = d.strs(v, "imports.forbid") },
		"allow":         func(v *strictyaml.Node) { c.Allow = d.strs(v, "imports.allow") },
		"allow_replace": func(v *strictyaml.Node) { c.AllowReplace = d.boolean(v, "imports.allow_replace") },
	})
	d.require(n, "imports", "module", "from", "forbid")
	return c
}

// checkImportInventories requires rules for the same Go module to agree on its
// package inventory and replace policy, because the module is checked once.
func (d *decoder) checkImportInventories(c *fileConfig) {
	byModule := map[string]*fileRule{}
	for _, r := range c.Rules {
		if r.Imports == nil {
			continue
		}
		prev, ok := byModule[r.Imports.Module]
		if !ok {
			byModule[r.Imports.Module] = r
			continue
		}
		d.rule = r.ID
		a, b := prev.Imports, r.Imports
		if a.HasPackages && b.HasPackages && !slices.Equal(sortedCopy(a.Packages), sortedCopy(b.Packages)) {
			d.errorf(b.packagesPos, "imports.packages for module %q differs from rule %q; rules for one module share one inventory", b.Module, prev.ID)
		}
		if b.HasPackages && !a.HasPackages {
			byModule[r.Imports.Module] = r
		}
		if a.AllowReplace != b.AllowReplace {
			d.errorf(b.modulePos, "imports.allow_replace for module %q differs from rule %q", b.Module, prev.ID)
		}
	}
	d.rule = ""
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

func (d *decoder) inlineSchema(n *strictyaml.Node, prev *schemaRef) *schemaRef {
	if prev != nil {
		d.errorf(at(n), "use schema or schema_file, not both")
		return prev
	}
	ref := &schemaRef{pos: at(n)}
	if n.Kind != strictyaml.Mapping {
		d.errorf(at(n), "schema must be a mapping")
		return ref
	}
	data, diags := n.JSON()
	for _, y := range diags {
		d.errorf(pos{y.Line, y.Column}, "schema: %s", y.Message)
	}
	if len(diags) > 0 {
		return ref
	}
	s, err := jsonschema.New(data)
	if err != nil {
		d.errorf(at(n), "schema: %v", err)
		return ref
	}
	ref.compiled = s
	return ref
}

func (d *decoder) schemaFile(n *strictyaml.Node, prev *schemaRef) *schemaRef {
	if prev != nil {
		d.errorf(at(n), "use schema or schema_file, not both")
		return prev
	}
	return &schemaRef{pos: at(n), File: d.path(n, "schema_file", false, false)}
}

// fields calls the handler for each key of a mapping, and reports unknown keys.
func (d *decoder) fields(n *strictyaml.Node, where string, handlers map[string]func(*strictyaml.Node)) {
	if n.Kind != strictyaml.Mapping {
		d.errorf(at(n), "%s must be a mapping, not %s", where, n.Kind)
		return
	}
	for _, p := range n.Pairs {
		h, ok := handlers[p.Key]
		if !ok {
			known := make([]string, 0, len(handlers))
			for k := range handlers {
				known = append(known, k)
			}
			slices.Sort(known)
			d.errorf(pos{p.Line, p.Column}, "%s has unknown field %q; known fields are %s", where, p.Key, strings.Join(known, ", "))
			continue
		}
		h(p.Value)
	}
}

// require reports keys missing from a mapping.
func (d *decoder) require(n *strictyaml.Node, where string, keys ...string) {
	if n.Kind != strictyaml.Mapping {
		return
	}
	for _, k := range keys {
		if !slices.ContainsFunc(n.Pairs, func(p strictyaml.Pair) bool { return p.Key == k }) {
			d.errorf(at(n), "%s is missing %s", where, k)
		}
	}
}

func (d *decoder) each(n *strictyaml.Node, where string, fn func(item *strictyaml.Node, where string)) {
	if n.Kind != strictyaml.Sequence {
		d.errorf(at(n), "%s must be a sequence, not %s", where, n.Kind)
		return
	}
	for i, item := range n.Items {
		fn(item, where+"["+strconv.Itoa(i)+"]")
	}
}

func (d *decoder) str(n *strictyaml.Node, where string) string {
	if n.Kind != strictyaml.String {
		d.errorf(at(n), "%s must be a string, not %s", where, n.Kind)
		return ""
	}
	return n.Value
}

func (d *decoder) strs(n *strictyaml.Node, where string) []string {
	var out []string
	d.each(n, where, func(item *strictyaml.Node, at2 string) {
		if item.Kind != strictyaml.String {
			d.errorf(at(item), "%s must be a string, not %s", at2, item.Kind)
			return
		}
		if slices.Contains(out, item.Value) {
			d.errorf(at(item), "%s repeats %q", where, item.Value)
			return
		}
		out = append(out, item.Value)
	})
	return out
}

func (d *decoder) path(n *strictyaml.Node, where string, glob, component bool) string {
	s := d.str(n, where)
	if n.Kind == strictyaml.String {
		if err := checkPath(s, glob, component); err != nil {
			d.errorf(at(n), "%s: %v", where, err)
		}
	}
	return s
}

func (d *decoder) paths(n *strictyaml.Node, where string, glob, component bool) []string {
	out := d.strs(n, where)
	if n.Kind == strictyaml.Sequence {
		for _, item := range n.Items {
			if item.Kind != strictyaml.String {
				continue
			}
			if err := checkPath(item.Value, glob, component); err != nil {
				d.errorf(at(item), "%s: %v", where, err)
			}
		}
	}
	return out
}

func (d *decoder) oneOf(n *strictyaml.Node, where string, values ...string) string {
	s := d.str(n, where)
	if n.Kind == strictyaml.String && !slices.Contains(values, s) {
		d.errorf(at(n), "%s is %q; use one of %s", where, s, strings.Join(values, ", "))
	}
	return s
}

func (d *decoder) integer(n *strictyaml.Node, where string) (int, bool) {
	if n.Kind == strictyaml.Number {
		if v, err := strconv.Atoi(n.Value); err == nil {
			return v, true
		}
	}
	d.errorf(at(n), "%s must be an integer", where)
	return 0, false
}

func (d *decoder) boolean(n *strictyaml.Node, where string) bool {
	if n.Kind != strictyaml.Bool {
		d.errorf(at(n), "%s must be true or false", where)
		return false
	}
	return strings.EqualFold(n.Value, "true")
}
