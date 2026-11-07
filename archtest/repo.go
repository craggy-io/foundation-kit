package archtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/craggy-io/foundation-kit/kind/jsonschema"
)

// CheckFile fails t if the repository breaks any rule in the archtest.yaml at path,
// listing every problem it finds. path is relative to the test's working directory,
// and the file's directory is the repository root that every path in it is relative
// to. A missing or invalid file fails the test without checking anything.
//
// Excluded paths and the exceptions that suppressed a problem are logged with t.Logf,
// so they show with go test -v.
func CheckFile(ctx context.Context, t testing.TB, path string) {
	t.Helper()
	diags, logs, name := evaluate(ctx, path, defaultLimits)
	for _, l := range logs {
		t.Logf("archtest: %s", l)
	}
	if len(diags) > 0 {
		t.Errorf("%s", report("archtest: "+name, diags, name))
	}
}

// limits bound the work CheckFile does, so a runaway tree fails rather than hangs.
type limits struct {
	entries    int   // enumerated files and directories
	fileBytes  int64 // bytes in one parsed file
	totalBytes int64 // bytes parsed in one run
}

var defaultLimits = limits{entries: 100_000, fileBytes: 16 << 20, totalBytes: 256 << 20}

type entryKind int

const (
	entryFile entryKind = iota + 1
	entryDir
)

func (k entryKind) String() string {
	if k == entryDir {
		return "directory"
	}
	return "file"
}

// entry is one regular file or directory in the checked tree.
type entry struct {
	path string // relative to the root, using "/"; "." for the root
	name string
	kind entryKind
}

// repo is one CheckFile run.
type repo struct {
	ctx    context.Context
	root   string // absolute OS path of the repository root
	config string // configuration file name, for diagnostics
	cfg    *fileConfig
	lim    limits

	entries  map[string]*entry
	children map[string][]*entry // directory path -> children, sorted by name
	order    []string            // every entry path, sorted
	excluded []string            // excluded paths that were pruned, for the log

	diags   []diagnostic
	logs    []string
	read    int64             // bytes read so far
	content map[string][]byte // file contents by path, nil if unreadable
	parsed  map[string]any    // parsed documents by family and path
}

// evaluate runs a configuration file and returns its sorted problems, log lines and
// the file's name.
func evaluate(ctx context.Context, path string, lim limits) ([]diagnostic, []string, string) {
	name := filepath.Base(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return []diagnostic{{Code: codeConfigInvalid, Path: name, Message: err.Error()}}, nil, name
	}
	r := &repo{ctx: ctx, root: filepath.Dir(abs), config: name, lim: lim,
		entries: map[string]*entry{}, children: map[string][]*entry{},
		content: map[string][]byte{}, parsed: map[string]any{}}
	r.run(abs)
	return sortDiagnostics(r.diags), r.logs, name
}

func (r *repo) run(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		r.diags = append(r.diags, diagnostic{Code: codeConfigInvalid, Path: r.config,
			Message: fmt.Sprintf("can't read the configuration: %v", err)})
		return
	}
	cfg, diags := decodeConfig(r.config, data)
	if len(diags) > 0 {
		r.diags = diags
		return
	}
	r.cfg = cfg
	if !r.resolveConfig() {
		return
	}
	if !r.walk() {
		return
	}
	for _, rule := range cfg.Rules {
		r.diags = append(r.diags, r.applyExceptions(rule, r.evaluateRule(rule))...)
	}
}

// configError records a problem with the configuration that is found only once the
// root is known.
func (r *repo) configError(code string, rule *fileRule, p pos, format string, args ...any) {
	d := diagnostic{Code: code, Path: r.config, Line: p.line, Column: p.column, Message: fmt.Sprintf(format, args...)}
	if rule != nil {
		d.Rule = rule.ID
	}
	r.diags = append(r.diags, d)
}

// resolveConfig loads schema files, checks Go modules exist and finds requirements
// aimed into excluded paths. It reports whether the configuration is usable.
func (r *repo) resolveConfig() bool {
	before := len(r.diags)
	for _, rule := range r.cfg.Rules {
		for _, ref := range rule.schemas() {
			if ref.File == "" {
				continue
			}
			p, err := r.resolveExact(ref.File, false)
			if err == nil {
				var data []byte
				if data, err = os.ReadFile(p); err == nil {
					ref.compiled, err = jsonschema.New(data)
				}
			}
			if err != nil {
				r.configError(codeConfigInvalid, rule, ref.pos, "schema_file %q: %v", ref.File, err)
			}
		}
		if imp := rule.Imports; imp != nil {
			if _, err := r.resolveExact(joinPath(imp.Module, "go.mod"), false); err != nil {
				r.configError(codeConfigInvalid, rule, imp.modulePos, "imports.module %q has no go.mod: %v", imp.Module, err)
			}
		}
		if s := rule.Select; s != nil {
			for _, inc := range s.Include {
				if isLiteral(inc) && r.isExcluded(inc) {
					r.configError(codeConfigConflict, rule, s.pos, "select.include %q is excluded at the root, so it can't be checked", inc)
				}
			}
		}
		for _, e := range rule.Exceptions {
			if r.isExcluded(e.Path) {
				r.configError(codeConfigConflict, rule, e.pos, "exception %q is excluded at the root", e.Path)
			}
		}
	}
	return len(r.diags) == before
}

func (rule *fileRule) schemas() []*schemaRef {
	var refs []*schemaRef
	if rule.Structured != nil && rule.Structured.Schema != nil {
		refs = append(refs, rule.Structured.Schema)
	}
	if rule.Markdown != nil && rule.Markdown.Frontmatter != nil && rule.Markdown.Frontmatter.Schema != nil {
		refs = append(refs, rule.Markdown.Frontmatter.Schema)
	}
	return refs
}

// isExcluded reports whether a path is removed by the root exclude list. ".git" at
// the root is always excluded.
func (r *repo) isExcluded(path string) bool {
	if path == ".git" || strings.HasPrefix(path, ".git/") {
		return true
	}
	for _, e := range r.cfg.Exclude {
		if matchPath(e.Path, path) {
			return true
		}
	}
	return false
}

// resolveExact returns the OS path of a repository-relative path whose every
// component exists with exactly that case, without following links. The last
// component must be a directory if wantDir, or a regular file otherwise.
func (r *repo) resolveExact(rel string, wantDir bool) (string, error) {
	dir := r.root
	if rel == "." {
		return dir, nil
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		j := slices.IndexFunc(entries, func(e fs.DirEntry) bool { return e.Name() == part })
		if j < 0 {
			return "", errors.New("not found (names are case-sensitive)")
		}
		last := i == len(parts)-1
		t := entries[j].Type()
		switch {
		case !last || wantDir:
			if !t.IsDir() {
				return "", fmt.Errorf("%q isn't a directory", strings.Join(parts[:i+1], "/"))
			}
		case !t.IsRegular():
			return "", errors.New("isn't a regular file")
		}
		dir = filepath.Join(dir, part)
	}
	return dir, nil
}

// walk enumerates the tree once, without following links, and reports whether every
// directory was read.
func (r *repo) walk() bool {
	r.entries["."] = &entry{path: ".", name: ".", kind: entryDir}
	r.order = append(r.order, ".")
	queue := []string{"."}
	count := 0
	for len(queue) > 0 {
		if err := r.ctx.Err(); err != nil {
			r.diags = append(r.diags, diagnostic{Code: codeIncomplete, Path: queue[0],
				Message: fmt.Sprintf("stopped before checking this and %d other directories: %v", len(queue)-1, err)})
			return false
		}
		dir := queue[0]
		queue = queue[1:]
		list, err := os.ReadDir(filepath.Join(r.root, filepath.FromSlash(dir)))
		if err != nil {
			r.diags = append(r.diags, diagnostic{Code: codeIncomplete, Path: dir,
				Message: fmt.Sprintf("can't read the directory, so it isn't checked: %v", err)})
			continue
		}
		folded := map[string]string{}
		for _, d := range list {
			name := d.Name()
			path := joinPath(dir, name)
			if other, dup := folded[strings.ToLower(name)]; dup {
				r.diags = append(r.diags, diagnostic{Code: codeCaseCollision, Path: path,
					Message: fmt.Sprintf("differs from %q only in case, which breaks on case-insensitive file systems", joinPath(dir, other))})
			}
			folded[strings.ToLower(name)] = name
			if r.isExcluded(path) {
				if path != ".git" {
					r.excluded = append(r.excluded, path)
				}
				continue
			}
			var k entryKind
			switch t := d.Type(); {
			case t.IsDir():
				k = entryDir
			case t.IsRegular():
				k = entryFile
			default:
				r.diags = append(r.diags, diagnostic{Code: codeEntryUnsupported, Path: path,
					Message: "symlinks, junctions and other special entries aren't followed or allowed; replace it with a regular file or directory, or exclude it"})
				continue
			}
			if count++; count > r.lim.entries {
				r.diags = append(r.diags, diagnostic{Code: codeLimitExceeded, Path: path,
					Message: fmt.Sprintf("the tree has more than %d entries, so the rest isn't checked; exclude generated or vendored trees", r.lim.entries)})
				return false
			}
			e := &entry{path: path, name: name, kind: k}
			r.entries[path] = e
			r.children[dir] = append(r.children[dir], e)
			r.order = append(r.order, path)
			if k == entryDir {
				queue = append(queue, path)
			}
		}
	}
	slices.Sort(r.order)
	for _, p := range r.excluded {
		r.logs = append(r.logs, fmt.Sprintf("excluded %s", p))
	}
	return true
}

// selectTargets resolves a rule's selector, reporting includes that match nothing.
func (r *repo) selectTargets(rule *fileRule) []*entry {
	s := rule.Select
	want := entryFile
	if s.Kind == kindDirectory {
		want = entryDir
	}
	matched := make([]int, len(s.Include))
	var out []*entry
	for _, p := range r.order {
		e := r.entries[p]
		if e.kind != want || matchAnyPath(s.Exclude, p) {
			continue
		}
		hit := false
		for i, inc := range s.Include {
			if matchPath(inc, p) {
				matched[i]++
				hit = true
			}
		}
		if hit {
			out = append(out, e)
		}
	}
	if !s.AllowEmpty {
		for i, inc := range s.Include {
			if matched[i] == 0 {
				r.configError(codeSelectorEmpty, rule, s.pos,
					"select.include %q matches no %s; fix the pattern, or set on_empty: allow with an empty_reason", inc, s.Kind)
			}
		}
	}
	return out
}

// evaluateRule runs one rule and returns its problems before exceptions.
func (r *repo) evaluateRule(rule *fileRule) []diagnostic {
	var ds []diagnostic
	switch {
	case rule.Imports != nil:
		ds = r.checkImports(rule)
	case rule.Tree != nil:
		for _, e := range r.selectTargets(rule) {
			ds = append(ds, r.checkTree(rule.Tree, e)...)
		}
	case rule.GoSource != nil:
		for _, e := range r.selectTargets(rule) {
			ds = append(ds, r.checkGoSource(rule.GoSource, e)...)
		}
	case rule.Structured != nil:
		for _, e := range r.selectTargets(rule) {
			ds = append(ds, r.checkStructured(rule.Structured, e)...)
		}
	case rule.Markdown != nil:
		for _, e := range r.selectTargets(rule) {
			ds = append(ds, r.checkMarkdown(rule.Markdown, e)...)
		}
	}
	for i := range ds {
		if ds[i].Rule == "" && ds[i].Code != codeIncomplete && ds[i].Code != codeLimitExceeded {
			ds[i].Rule = rule.ID
		}
		if ds[i].Rule == rule.ID {
			ds[i].Why = rule.Why
			ds[i].RuleLine, ds[i].RuleColumn = rule.line, rule.column
		}
	}
	return ds
}

// applyExceptions drops a rule's problems on its exception paths, and reports
// exceptions that suppressed nothing.
func (r *repo) applyExceptions(rule *fileRule, ds []diagnostic) []diagnostic {
	if len(rule.Exceptions) == 0 {
		return ds
	}
	kept := ds[:0]
	for _, d := range ds {
		i := slices.IndexFunc(rule.Exceptions, func(e *exception) bool { return d.Rule == rule.ID && e.Path == d.Path })
		if i < 0 {
			kept = append(kept, d)
			continue
		}
		e := rule.Exceptions[i]
		e.used = true
		r.logs = append(r.logs, fmt.Sprintf("[%s] exception for %s suppressed %s: %s", rule.ID, e.Path, d.Code, e.Why))
	}
	for _, e := range rule.Exceptions {
		if !e.used {
			kept = append(kept, diagnostic{Code: codeExceptionStale, Rule: rule.ID, Path: r.config,
				Line: e.line, Column: e.column,
				Message: fmt.Sprintf("the exception for %q suppresses nothing; remove it", e.Path)})
		}
	}
	return kept
}

// readFile returns a selected file's content, reading it once per run. It verifies
// that the file is still a regular file in the root, and enforces the size limits.
// On failure it records a problem and returns false.
func (r *repo) readFile(e *entry) ([]byte, bool) {
	if data, ok := r.content[e.path]; ok {
		return data, data != nil
	}
	data, d := r.readFresh(e)
	if d != nil {
		r.diags = append(r.diags, *d)
	}
	r.content[e.path] = data
	return data, data != nil
}

func (r *repo) readFresh(e *entry) ([]byte, *diagnostic) {
	fail := func(code, format string, args ...any) ([]byte, *diagnostic) {
		return nil, &diagnostic{Code: code, Path: e.path, Message: fmt.Sprintf(format, args...)}
	}
	if err := r.ctx.Err(); err != nil {
		return fail(codeIncomplete, "not checked: %v", err)
	}
	osPath := filepath.Join(r.root, filepath.FromSlash(e.path))
	before, err := os.Lstat(osPath)
	if err != nil {
		return fail(codeIncomplete, "can't read the file, so it isn't checked: %v", err)
	}
	if !before.Mode().IsRegular() {
		return fail(codeIncomplete, "changed into a %v during the check; use an immutable checkout", before.Mode().Type())
	}
	f, err := os.Open(osPath)
	if err != nil {
		return fail(codeIncomplete, "can't read the file, so it isn't checked: %v", err)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return fail(codeIncomplete, "was replaced during the check; use an immutable checkout")
	}
	if after.Size() > r.lim.fileBytes {
		return fail(codeLimitExceeded, "is %d bytes, more than the %d a parsed file may have", after.Size(), r.lim.fileBytes)
	}
	if r.read+after.Size() > r.lim.totalBytes {
		return fail(codeLimitExceeded, "not checked: the run has already parsed %d bytes, near the %d limit", r.read, r.lim.totalBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, r.lim.fileBytes+1))
	if err != nil {
		return fail(codeIncomplete, "can't read the file, so it isn't checked: %v", err)
	}
	if int64(len(data)) > r.lim.fileBytes {
		return fail(codeLimitExceeded, "grew past the %d bytes a parsed file may have", r.lim.fileBytes)
	}
	r.read += int64(len(data))
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

// checkImports runs one imports rule's Go module through the same evaluator as Check.
// Every imports rule for a module is evaluated in one scan, by the first rule naming
// it; the others contribute their rules to that scan and return nothing themselves.
func (r *repo) checkImports(rule *fileRule) []diagnostic {
	module := rule.Imports.Module
	var group []*fileRule
	for _, other := range r.cfg.Rules {
		if other.Imports != nil && other.Imports.Module == module {
			group = append(group, other)
		}
	}
	if group[0] != rule {
		return nil
	}
	c := Config{Dir: filepath.Join(r.root, filepath.FromSlash(module))}
	byName := map[string]*fileRule{}
	inventory := rule // the rule whose packages list is used, for positions
	for _, g := range group {
		imp := g.Imports
		c.Rules = append(c.Rules, Rule{Name: g.ID, Why: g.Why, From: imp.From, Except: imp.Except, Forbid: imp.Forbid, Allow: imp.Allow})
		if imp.HasPackages {
			c.Packages, inventory = imp.Packages, g
		}
		c.AllowReplace = imp.AllowReplace
		byName[g.ID] = g
	}
	ds, err := run(c)
	if err != nil {
		return []diagnostic{{Code: codeIncomplete, Path: module, Message: fmt.Sprintf("can't check the Go module's imports: %v", err)}}
	}
	for i := range ds {
		d := &ds[i]
		switch {
		case d.Path != "":
			d.Path = joinPath(module, d.Path)
		case byName[d.Rule] != nil:
			// A problem with the rule itself points at the rule.
			d.Path, d.Line, d.Column = r.config, byName[d.Rule].line, byName[d.Rule].column
		default:
			// A problem with the inventory points at its packages list.
			d.Path, d.Line, d.Column = r.config, inventory.Imports.packagesPos.line, inventory.Imports.packagesPos.column
		}
		if d.Rule == "" {
			// Module-wide problems (coverage, replace, unparseable files) belong to the
			// rule holding the inventory.
			d.Rule = inventory.ID
		}
		g := byName[d.Rule]
		d.Why, d.RuleLine, d.RuleColumn = g.Why, g.line, g.column
	}
	return ds
}
